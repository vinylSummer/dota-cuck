#!/usr/bin/env python3
"""OCR-gated GUI automation: Dota dashboard -> live-spectate a friend (player view).

There is NO console command that joins a live match by id (`dota_spectate_game` does not exist in
this build). Live friend-spectating is GC-mediated through the GUI (validated live 2026-06-24):
the friends panel is docked open on the LEFT with an "IN DOTA (n)" list; right-click the friend who
is in a live match -> a context menu opens -> click WATCH FRIEND LIVE (the Dota+ low-latency
option, preferred) or WATCH GAME (the standard delayed fallback). The native client then does the
GC handshake / SDR ticket / connect / render; we only drive the mouse and keyboard. WATCH FRIEND
LIVE lands directly in PLAYER VIEW (the friend's own camera), which is the desired output — so
there is NO in-session camera-follow step.

GUI click automation is fragile, so every step is OCR-gated: `precondition -> action ->
postcondition`, with bounded retries and an `on_unexpected()` recovery hook on any failed gate.
Element location is HYBRID — OCR word-boxes (tesseract TSV) for dynamic text (the friend's row,
context-menu items) and RATIO ANCHORS (fractions of SCREEN_W/SCREEN_H, calibrated once) for
icon-only chrome that has no text (the friends button, a modal's close-X). Everything is
resolution-parametric: no pixel is hardcoded; coordinates are OCR boxes or ratios of the
configured screen size (default 1280x720, the validated NVENC/stream target).

This runs INSIDE the dota-v5 container (it has tesseract/imagemagick + the X server + the input
FIFOs) as the `worker` user, so it can read the worker-owned input FIFOs and reach DISPLAY :99.
  - mouse: writes `Pointer <cnt> <x> <y> <mask>` lines to the existing VNC FIFO, reusing the
    libinput-bound `dota-vnc-mouse` absolute device (no new uinput device is created here).
  - console: writes command lines to the `uinput_daemon` FIFO (the `dota-spectate-uinput` keyboard).
  - OCR: convert | tesseract on an ffmpeg x11grab still, the same preprocess as ocr.sh.

The pure decision logic (modal classification, box-finding, fuzzy matching) is split out so Phase B
can port it into worker/dota_client.py and unit-test it against fixture OCR strings.

Usage:
  gui_spectate.py spectate --target-name "<persona>"        # full path + NVENC capture
  gui_spectate.py click-ratio RX RY [--button left|right]   # calibration probe
  gui_spectate.py ocr [--region WxH+X+Y] [--psm N]          # dump OCR of a still
"""
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time
from dataclasses import dataclass, field
from difflib import SequenceMatcher

DISPLAY = os.environ.get("DISPLAY", ":99")
_w, _, _h = os.environ.get("SCREEN", "1280x720").partition("x")
SCREEN_W = int(_w or 1280)
SCREEN_H = int(_h or 720)

SHOTDIR = os.environ.get("SHOTDIR", "/fard/steam/steamhome/v5-shots")
LOGDIR = os.path.join(SHOTDIR, "gui_spectate")
VNC_FIFO = os.environ.get("VNC_FIFO", "/tmp/vnc_input.fifo")
UINPUT_FIFO = os.environ.get("UINPUT_FIFO", "/tmp/dota_uinput.fifo")
# Where the ratio anchors for icon-only chrome are recorded by the calibration pass. Falls back to
# the (clearly-flagged) DEFAULT_ANCHORS below, which are approximate and MUST be calibrated.
CALIB_FILE = os.environ.get("GUI_CALIB", os.path.join(SHOTDIR, "gui_spectate.calib.json"))

# Icon-only chrome that OCR can't see, as (rx, ry) fractions of the screen. APPROXIMATE — overwrite
# via the calib file (gui_spectate.py click-ratio to find them on a live VNC session). Loudly warned
# about at runtime so an uncalibrated run can never silently misclick.
DEFAULT_ANCHORS = {
    "friends_button": (0.965, 0.022),     # social/friends toggle, far top-right
    "behavior_close": (0.745, 0.205),     # Player Behavior Summary close-X (ESC/ENTER don't work)
    "dota_plus_close": (0.745, 0.205),    # Welcome-to-Dota-Plus dismiss
    "update_ok": (0.498, 0.565),          # "Update Required" OK button (validated live 2026-06-24)
}


def _log(msg: str) -> None:
    sys.stderr.write(f"[gui_spectate] {msg}\n")
    sys.stderr.flush()


# ============================ pure decision logic (ported in Phase B) ============================

# Catalogued OCR state signatures (anchor substrings, normalized). Order matters: a modal title is
# checked before the bare-dashboard PLAY DOTA so a modal sitting over the dashboard is caught.
MODAL_SIGNATURES = [
    ("UPDATE_REQUIRED", ["out of date", "update required"]),
    ("BEHAVIOR_SUMMARY", ["player behavior summary", "behavior summary"]),
    ("PARTY_INVITE", ["party invitation"]),
    ("DOTA_PLUS", ["welcome to dota plus", "dota plus"]),
]
DASHBOARD_SIGNATURE = ["play dota"]


def _norm(s: str) -> str:
    return " ".join(s.lower().split())


def classify_state(ocr_text: str) -> str:
    """Map a full-screen OCR dump to a known state. Pure: drives CLEAR_MODALS + the dashboard gate.

    Returns one of the MODAL_SIGNATURES keys, "DASHBOARD" (bare main menu, ready), or "UNKNOWN".
    A modal sitting over the dashboard classifies as the modal (checked first) so it gets cleared.
    """
    t = _norm(ocr_text)
    for state, needles in MODAL_SIGNATURES:
        if any(n in t for n in needles):
            return state
    if any(n in t for n in DASHBOARD_SIGNATURE):
        return "DASHBOARD"
    return "UNKNOWN"


# Dota's stylized Panorama font drives a few stable tesseract confusions (measured on nav tabs);
# fold them before fuzzy comparison so a friend's name still matches its OCR.
_FONT_CONFUSIONS = str.maketrans({"K": "R", "I": "T", "0": "O", "1": "I", "5": "S", "8": "B"})


def _fuzzy_key(s: str) -> str:
    return _norm(s).translate(_FONT_CONFUSIONS)


def fuzzy_equal(a: str, b: str, threshold: float = 0.7) -> bool:
    """Confusion-tolerant string match for OCR'd UI text vs an expected label/name. Pure."""
    ka, kb = _fuzzy_key(a), _fuzzy_key(b)
    if not ka or not kb:
        return False
    if ka in kb or kb in ka:
        return True
    return SequenceMatcher(None, ka, kb).ratio() >= threshold


@dataclass
class Box:
    """A tesseract word box mapped back to SCREEN pixels (center + bounds), with its text + conf."""
    text: str
    cx: int
    cy: int
    left: int
    top: int
    width: int
    height: int
    conf: float


def parse_tsv(tsv: str, region_x: int = 0, region_y: int = 0, scale: float = 2.0) -> list[Box]:
    """Parse tesseract TSV into screen-space Boxes. Pure (no I/O), so it's unit-testable.

    The OCR pipeline upscales `scale`x after cropping to `region`, so a TSV coordinate maps back to
    a screen pixel as  region_origin + tsv_coord / scale.
    """
    boxes: list[Box] = []
    lines = tsv.splitlines()
    if not lines:
        return boxes
    header = lines[0].split("\t")
    try:
        idx = {k: header.index(k) for k in
               ("left", "top", "width", "height", "conf", "text")}
    except ValueError:
        return boxes  # not a TSV header (e.g. plain-text output)
    for ln in lines[1:]:
        cols = ln.split("\t")
        if len(cols) <= idx["text"]:
            continue
        text = cols[idx["text"]].strip()
        if not text:
            continue
        try:
            left = int(cols[idx["left"]]); top = int(cols[idx["top"]])
            w = int(cols[idx["width"]]); h = int(cols[idx["height"]])
            conf = float(cols[idx["conf"]])
        except ValueError:
            continue
        if conf < 0:                      # tesseract emits -1 for non-word rows
            continue
        sl = region_x + int(left / scale); st = region_y + int(top / scale)
        sw = int(w / scale); sh = int(h / scale)
        boxes.append(Box(text, sl + sw // 2, st + sh // 2, sl, st, sw, sh, conf))
    return boxes


def find_text_box(boxes: list[Box], target: str, min_conf: float = 40.0) -> Box | None:
    """Best fuzzy match for `target` among word boxes. Pure. Returns the box (or merged box) to
    click, or None.

    Tries, in order: (1) the full multi-word window, then (2) a single DISTINCTIVE target word
    (len>=4) against any box. The single-word fallback matters because the stylized Dota font mangles
    part of a persona badly (validated 2026-06-24: "zitraks mops" OCR'd "ntrake mops" — the first
    word is wrecked but "mops" reads clean), and clicking anywhere on the friend's row works."""
    target_words = _norm(target).split()
    n = len(target_words)
    usable = [b for b in boxes if b.conf >= min_conf]

    best: tuple[float, Box] | None = None
    for i in range(len(usable)):
        window = usable[i:i + n]
        if len(window) < n:
            break
        phrase = " ".join(b.text for b in window)
        ratio = SequenceMatcher(None, _fuzzy_key(phrase), _fuzzy_key(target)).ratio()
        if fuzzy_equal(phrase, target) or ratio >= 0.7:
            merged = _merge_boxes(window)
            if best is None or ratio > best[0]:
                best = (ratio, merged)
    if best:
        return best[1]

    # fallback: a single distinctive word (>=4 chars) of the target, exact-ish on one box
    for word in sorted((w for w in target_words if len(w) >= 4), key=len, reverse=True):
        for b in usable:
            if SequenceMatcher(None, _fuzzy_key(b.text), _fuzzy_key(word)).ratio() >= 0.8:
                return b
    return None


def _merge_boxes(window: list[Box]) -> Box:
    left = min(b.left for b in window)
    top = min(b.top for b in window)
    right = max(b.left + b.width for b in window)
    bottom = max(b.top + b.height for b in window)
    conf = min(b.conf for b in window)
    text = " ".join(b.text for b in window)
    return Box(text, (left + right) // 2, (top + bottom) // 2,
               left, top, right - left, bottom - top, conf)


# ============================== I/O: screenshots, OCR, mouse, console ==============================

@dataclass
class Context:
    """Run-scoped wiring + the calibrated anchors. Bundled so the step functions stay testable-ish
    (the pure helpers above take plain strings; only this side touches the device/FS)."""
    anchors: dict
    counter: list = field(default_factory=lambda: [0])
    using_default_anchors: bool = True
    pos: list = field(default_factory=lambda: [SCREEN_W // 2, SCREEN_H // 2])  # last cursor pos


def load_anchors() -> tuple[dict, bool]:
    anchors = dict(DEFAULT_ANCHORS)
    used_default = True
    try:
        with open(CALIB_FILE) as f:
            data = json.load(f)
        anchors.update({k: tuple(v) for k, v in data.items()})
        used_default = False
        _log(f"loaded calibrated anchors from {CALIB_FILE}: {sorted(data)}")
    except FileNotFoundError:
        _log(f"WARNING: no calib file at {CALIB_FILE} — using APPROXIMATE default anchors "
             f"{sorted(DEFAULT_ANCHORS)}; clicks on icon-only chrome may miss. Calibrate first.")
    except Exception as ex:  # noqa: BLE001
        _log(f"WARNING: bad calib file {CALIB_FILE}: {ex}; using defaults.")
    return anchors, used_default


def screenshot(name: str) -> str:
    """Grab a single frame of :99 to SHOTDIR/<name>.png and return the path."""
    os.makedirs(SHOTDIR, exist_ok=True)
    path = os.path.join(SHOTDIR, f"{name}.png")
    subprocess.run(
        ["ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
         "-f", "x11grab", "-i", DISPLAY, "-frames:v", "1", path],
        check=False, env={**os.environ, "DISPLAY": DISPLAY},
    )
    return path


def _convert_tesseract(img: str, region: str | None, psm: int, tsv: bool,
                       invert: bool = False, scale: float = 2.0) -> str:
    crop = ["-crop", region, "+repage"] if region else []
    # The Panorama context menu is light-grey text on a near-black panel; tesseract reads dark text
    # on light far better, so `invert` negates before thresholding (validated on the WATCH menu).
    # The menu text is also small, so it needs a larger `scale` (3x) than the dashboard (2x) to read.
    neg = ["-negate"] if invert else []
    resize = f"{int(scale * 100)}%"
    # 50% reads the high-contrast dashboard/friend text (validated); the inverted menu (light-grey
    # on near-black, after negate) needs a higher 60% cutoff. Keep these separate so tuning the menu
    # never regresses friend-row location.
    thresh = "60%" if invert else "50%"
    conv = subprocess.Popen(
        ["convert", img, *crop, "-colorspace", "Gray", *neg, "-resize", resize,
         "-threshold", thresh, "png:-"],
        stdout=subprocess.PIPE,
    )
    args = ["tesseract", "stdin", "stdout", "--psm", str(psm)]
    if tsv:
        args.append("tsv")
    out = subprocess.run(args, stdin=conv.stdout, stdout=subprocess.PIPE,
                         stderr=subprocess.DEVNULL, text=True)
    conv.stdout.close()
    conv.wait()
    return out.stdout


def ocr_text(name: str, region: tuple[int, int, int, int] | None = None, psm: int | None = None) -> str:
    """OCR a fresh screenshot (full screen, or a (x,y,w,h) region) to plain text.

    Full-screen state detection uses psm 11 (sparse scattered UI text); a cropped region defaults
    to psm 6 (uniform block — best for modal copy + menu items, per the catalogued signatures)."""
    img = screenshot(name)
    reg = f"{region[2]}x{region[3]}+{region[0]}+{region[1]}" if region else None
    p = psm if psm is not None else (6 if region else 11)
    return _convert_tesseract(img, reg, p, tsv=False)


def ocr_boxes(name: str, region: tuple[int, int, int, int] | None = None, psm: int = 6,
              invert: bool = False, scale: float = 2.0) -> list[Box]:
    """OCR a region (or full screen) and return screen-space word Boxes. `invert` for light-on-dark
    UI text (the context menu); `scale` is the upscale factor (3x for the small menu text)."""
    img = screenshot(name)
    rx, ry = (region[0], region[1]) if region else (0, 0)
    reg = f"{region[2]}x{region[3]}+{region[0]}+{region[1]}" if region else None
    tsv = _convert_tesseract(img, reg, psm, tsv=True, invert=invert, scale=scale)
    return parse_tsv(tsv, region_x=rx, region_y=ry, scale=scale)


def _fifo_write(path: str, line: str) -> None:
    with open(path, "w") as f:
        f.write(line + "\n")


def _pointer(ctx: Context, x: int, y: int, mask: int) -> None:
    ctx.counter[0] += 1
    x = max(0, min(SCREEN_W - 1, x)); y = max(0, min(SCREEN_H - 1, y))
    _fifo_write(VNC_FIFO, f"Pointer {ctx.counter[0]} {x} {y} {mask}")


# VALIDATED LIVE (2026-06-24): Panorama only registers a hover / opens a context menu if the
# pointer ARRIVES via continuous motion. A single absolute jump to (x,y) then a click moves the X
# cursor (xdotool confirms the position) and the button reaches the X server (RawButtonPress seen)
# but the UI does NOT react — the menu never opens, the row never highlights. Sending a dense path
# of intermediate points (~15 steps @ ~30ms) before the click is what makes clicks land. So `move`
# always interpolates from the last position; `click` relies on it.
_MOVE_STEPS = 15
_MOVE_STEP_DELAY = 0.03


def move(ctx: Context, x: int, y: int) -> None:
    x = max(0, min(SCREEN_W - 1, x)); y = max(0, min(SCREEN_H - 1, y))
    x0, y0 = ctx.pos
    for i in range(1, _MOVE_STEPS + 1):
        ix = round(x0 + (x - x0) * i / _MOVE_STEPS)
        iy = round(y0 + (y - y0) * i / _MOVE_STEPS)
        _pointer(ctx, ix, iy, 0)
        time.sleep(_MOVE_STEP_DELAY)
    ctx.pos[0], ctx.pos[1] = x, y
    time.sleep(0.3)   # let the hover settle before any click


def click(ctx: Context, x: int, y: int, button: str = "left") -> None:
    mask = 1 if button == "left" else 4   # bit0 left, bit2 right (per vnc_input_daemon BTN_BITS)
    move(ctx, x, y)                        # dense motion — required for the click to register
    _pointer(ctx, x, y, mask)
    time.sleep(0.12)
    _pointer(ctx, x, y, 0)
    time.sleep(0.6)
    _log(f"{button}-click @ {x},{y}")


def click_ratio(ctx: Context, rx: float, ry: float, button: str = "left") -> tuple[int, int]:
    x, y = int(rx * SCREEN_W), int(ry * SCREEN_H)
    click(ctx, x, y, button)
    return x, y


def click_anchor(ctx: Context, name: str, button: str = "left") -> tuple[int, int]:
    rx, ry = ctx.anchors[name]
    return click_ratio(ctx, rx, ry, button)


def focus_dota() -> None:
    subprocess.run(
        ["bash", "-lc",
         'WIN=$(xdotool search --class dota2 | head -1); '
         '[ -n "$WIN" ] && xdotool windowactivate --sync "$WIN" 2>/dev/null || true'],
        check=False, env={**os.environ, "DISPLAY": DISPLAY},
    )


def raise_dota() -> None:
    """Bring the Dota window to the front + focus it before driving the mouse. The GUI Steam client
    can pop a friend-chat window on top of Dota (validated 2026-06-24); without raising Dota, clicks
    would land on the Steam window instead. Also closes any Steam chat popup that's covering it."""
    subprocess.run(
        ["bash", "-lc",
         # close transient Steam CHAT popups (title = friend name) but NOT the main "Steam" window
         # (Dota needs the running client for auth) nor the xfce panels (desktop -1); then raise Dota.
         'wmctrl -l | awk \'$2==0 && $4!="Dota" && $4!="Steam" {print $1}\' | '
         '  while read w; do wmctrl -ic "$w" 2>/dev/null || true; done; '
         'WIN=$(xdotool search --class dota2 | head -1); '
         '[ -n "$WIN" ] && { xdotool windowactivate --sync "$WIN"; xdotool windowraise "$WIN"; } '
         '2>/dev/null || true'],
        check=False, env={**os.environ, "DISPLAY": DISPLAY},
    )
    time.sleep(1.0)


def console(cmd: str) -> None:
    """Send a console command through the persistent uinput keyboard daemon (opens grave, types,
    enter, closes grave)."""
    focus_dota()
    _fifo_write(UINPUT_FIFO, cmd)
    time.sleep(4.0)   # daemon's grave/type/enter/grave sequence + game reaction
    _log(f"console: {cmd}")


# ===================================== recovery hook (log-only) ===================================

def on_unexpected(step: str, expected: str, ocr_dump: str, shot: str) -> str:
    """Pluggable recovery seam. V1 is LOG-ONLY: persist the screenshot + full OCR dump + the
    expected-vs-actual + step so an unexpected game state is never silently clicked through. Returns
    RETRY or ABORT. A real vision model drops in later via vision_classify() WITHOUT touching the
    state machine.
    """
    os.makedirs(LOGDIR, exist_ok=True)
    stamp = time.strftime("%Y%m%d-%H%M%S")
    base = os.path.join(LOGDIR, f"{stamp}-{step}")
    try:
        if os.path.exists(shot):
            os.replace(shot, base + ".png")
        with open(base + ".txt", "w") as f:
            f.write(f"step={step}\nexpected={expected}\n\n--- OCR ---\n{ocr_dump}\n")
    except Exception as ex:  # noqa: BLE001
        _log(f"on_unexpected: failed to persist dump: {ex}")
    decision = vision_classify(base + ".png")
    _log(f"on_unexpected[{step}] expected={expected!r}; logged to {base}.*; decision={decision}")
    return decision


def vision_classify(shot_path: str) -> str:
    """Seam for a future vision model: classify an unexpected screen + suggest a recovery action.
    Stubbed to ABORT in V1 (log-only). When wired, return RETRY for a recoverable, known overlay."""
    return "ABORT"


# ========================================= the step machine =======================================

class SpectateAborted(Exception):
    pass


def _gate(ctx: Context, step: str, expected: str, check, *, tries: int = 6, delay: float = 2.0) -> str:
    """Re-OCR until `check(text) -> True`, else hand the last frame to on_unexpected. `check`
    returns the value to propagate on success (truthy)."""
    last_text = ""
    for attempt in range(1, tries + 1):
        name = f"gate-{step.lower()}-{attempt}"
        last_text = ocr_text(name)
        result = check(last_text)
        if result:
            _log(f"{step}: gate satisfied (attempt {attempt})")
            return result
        time.sleep(delay)
    shot = os.path.join(SHOTDIR, f"gate-{step.lower()}-{tries}.png")
    if on_unexpected(step, expected, last_text, shot) == "RETRY":
        return _gate(ctx, step, expected, check, tries=2, delay=delay)
    raise SpectateAborted(f"{step}: gate {expected!r} never satisfied")


def dashboard_ready(name: str = "dash-check") -> bool:
    """Robust 'main menu ready' detector. Full-screen psm-11 OCR misses the stylized PLAY DOTA
    button (validated 2026-06-24: dashboard up, but classify_state read UNKNOWN), so check two
    reliable signals: (a) the PLAY DOTA button via a bottom-right corner crop at psm 7 (the
    catalogued anchor), or (b) the friends panel signature (IN DOTA / SEARCH FRIENDS)."""
    btn_region = (int(0.78 * SCREEN_W), int(0.88 * SCREEN_H), int(0.22 * SCREEN_W),
                  int(0.12 * SCREEN_H))
    if "play dota" in _norm(ocr_text(name, region=btn_region, psm=7)):
        return True
    return _panel_open(ocr_text(name + "-panel", region=_friends_region()))


def step_dashboard(ctx: Context) -> None:
    """1 DASHBOARD + 2 CLEAR_MODALS: loop clearing known first-login modals until the bare
    dashboard (PLAY DOTA / friends panel, no known modal) is showing."""
    for _ in range(8):
        text = ocr_text("state")
        state = classify_state(text)
        _log(f"state = {state}")
        if state == "DASHBOARD":
            return
        if state == "BEHAVIOR_SUMMARY":
            click_anchor(ctx, "behavior_close")          # ESC/ENTER do NOT dismiss this one
        elif state == "PARTY_INVITE":
            console("RAW KEY_ESC")                        # ESC (or DECLINE box) dismisses
        elif state == "DOTA_PLUS":
            click_anchor(ctx, "dota_plus_close")
        elif state == "UPDATE_REQUIRED":
            # An out-of-date client CAN still spectate live matches, so this is non-fatal — just
            # dismiss it by clicking OK (validated live 2026-06-24). steamcmd keeps the install
            # current in production, so this modal is rare anyway.
            click_anchor(ctx, "update_ok")
        else:  # UNKNOWN to the full-screen classifier — confirm via the robust dashboard check
            if dashboard_ready():
                _log("dashboard ready (corner PLAY DOTA / friends panel)")
                return
            if on_unexpected("CLEAR_MODALS", "PLAY DOTA / known modal", text,
                             os.path.join(SHOTDIR, "state.png")) != "RETRY":
                raise SpectateAborted("unknown overlay at dashboard")
        time.sleep(1.5)
    # final gate
    _gate(ctx, "DASHBOARD", "PLAY DOTA / friends panel",
          lambda _t: dashboard_ready("gate-dashboard") and "DASHBOARD")


# The friends panel is docked OPEN on the LEFT (validated 2026-06-24: ~x 0..0.22*W), with an
# "IN DOTA (n)" section listing friends in the Dota client. Scope OCR to that strip so a friend's
# name doesn't collide with dashboard text. Region is resolution-parametric (ratios of the screen).
def _friends_region() -> tuple[int, int, int, int]:
    return (0, 0, int(0.22 * SCREEN_W), SCREEN_H)


def _panel_open(text: str) -> bool:
    n = _norm(text)
    return "in dota" in n or "search friends" in n or "add friend" in n


def step_open_friends(ctx: Context) -> None:
    """3 OPEN_FRIENDS: on this build the friends panel is DOCKED OPEN on the left by default, and the
    panel header ("IN DOTA"/"SEARCH FRIENDS") OCRs unreliably from the busy full-height strip, so
    this is detection-only — it never clicks the (uncalibrated) friends button and never hard-aborts.
    LOCATE_FRIEND is the real gate: if the target's row is found, the panel was open. (A build that
    defaults the panel closed would need a calibrated friends_button anchor + a click here.)"""
    if _panel_open(ocr_text("friends-check", region=_friends_region())):
        _log("friends panel detected open (docked left)")
    else:
        _log("friends-panel header not OCR'd (busy strip); proceeding — panel is docked open by "
             "default, LOCATE_FRIEND will confirm")


# Context-menu item labels, in PREFERENCE order: WATCH FRIEND LIVE is the Dota+ low-latency option
# (use it when present); WATCH GAME is the standard delayed fallback shown for any live match.
SPECTATE_LABELS = ("Watch Friend Live", "Watch Game")


def _scroll_friends_to_top(ctx: Context) -> None:
    """Wheel the friends list UP to the top so the IN-DOTA section (always at the top) is in view.
    The in-match friends sit at the TOP of the list, so a down-scroll hides them (validated
    2026-06-24: a down-scroll-on-miss pushed every IN-DOTA friend off-screen). This resets any prior
    scroll instead."""
    region = _friends_region()
    cx = region[0] + region[2] // 2
    move(ctx, cx, SCREEN_H // 2)
    for _ in range(8):                              # wheel-up edges (bit3) to reach the top
        _pointer(ctx, cx, SCREEN_H // 2, 1 << 3)
        _pointer(ctx, cx, SCREEN_H // 2, 0)
        time.sleep(0.05)
    time.sleep(0.6)


def step_locate_and_spectate(ctx: Context, target_name: str) -> None:
    """4 LOCATE_FRIEND + 5 SPECTATE: find the target's row in the IN-DOTA list by OCR, right-click
    it to open the context menu, then click WATCH FRIEND LIVE (preferred) or WATCH GAME."""
    region = _friends_region()
    _scroll_friends_to_top(ctx)                     # ensure the IN-DOTA friends are visible at top
    target_box: Box | None = None
    for attempt in range(1, 6):
        # friend-row OCR is flaky (stylized/unicode personas, small text between avatars), so just
        # RE-OCR on a miss — do NOT scroll down (the target is at the top). psm 11 (sparse scattered
        # text) + 3x upscale reads the IN-DOTA rows; psm 6 / 2x miss them (validated 2026-06-24:
        # "zitraks mops" only OCRs as "ritraks mops" under psm 11 @ 300%).
        boxes = ocr_boxes(f"friends-{attempt}", region=region, psm=11, scale=3.0)
        target_box = find_text_box(boxes, target_name)
        if target_box:
            break
        time.sleep(1.0)
    if not target_box:
        shot = os.path.join(SHOTDIR, "friends-4.png")
        on_unexpected("LOCATE_FRIEND", f"friend row {target_name!r}",
                      ocr_text("friends-text", region=region), shot)
        raise SpectateAborted(f"friend {target_name!r} not found in IN-DOTA list")
    _log(f"target {target_name!r} row @ {target_box.cx},{target_box.cy} (conf {target_box.conf})")

    click(ctx, target_box.cx, target_box.cy, button="right")
    time.sleep(2.0)   # let the context menu fully open/animate before OCR (validated: too-fast misses)
    # The context menu's position is VARIABLE (validated 2026-06-24): sometimes it opens over the
    # LEFT friends panel (items ~x<280), sometimes to the RIGHT of the panel (items ~x280..520,
    # lower down). So scope OCR to a WIDE region spanning both — the left strip through the mid-left
    # of the screen. Light-grey text on near-black + small font → invert + 3x upscale. The WATCH
    # labels are distinctive multi-word phrases, so the wider scan doesn't false-match dashboard art.
    menu_region = (0, int(0.10 * SCREEN_H), int(0.45 * SCREEN_W), int(0.85 * SCREEN_H))

    def find_spectate(_t: str):
        boxes = ocr_boxes("menu", region=menu_region, invert=True, scale=3.0)
        for label in SPECTATE_LABELS:
            b = find_text_box(boxes, label)
            if b:
                return b
        return None

    item = _gate(ctx, "SPECTATE", "WATCH FRIEND LIVE / WATCH GAME menu item", find_spectate,
                 tries=5, delay=1.2)
    click(ctx, item.cx, item.cy, button="left")
    _log(f"clicked spectate item {item.text!r} @ {item.cx},{item.cy}")


JOIN_SETTLE_SECONDS = 12   # asset/map load after the dashboard tears down, before capture


def step_join_wait(ctx: Context) -> None:
    """6 JOIN_WAIT: clicking WATCH FRIEND LIVE tears the dashboard down to a dark loading screen,
    then the live match renders. Gate on the dashboard being GONE (validated: PLAY DOTA disappears),
    then settle for the map/asset load. WATCH FRIEND LIVE lands directly in PLAYER VIEW — the
    friend's own camera — which is the desired output, so there is NO camera-follow step.

    (An in-match-HUD OCR gate is intentionally avoided: in player view the HUD text is small and
    OCR-hostile; the dashboard's absence + the non-trivial moving capture are the reliable signals.)
    """
    def left_dashboard(t: str):
        return ("play dota" not in _norm(t)) and True
    _gate(ctx, "JOIN_WAIT", "dashboard torn down (entering spectate)",
          left_dashboard, tries=20, delay=3.0)
    _log(f"left dashboard; settling {JOIN_SETTLE_SECONDS}s for the live match to load")
    time.sleep(JOIN_SETTLE_SECONDS)


def capture(seconds: int = 6) -> int:
    """8 CAPTURE: short NVENC clip proving a MOVING live render (not the menu). Returns byte size."""
    out = os.path.join(SHOTDIR, "v5_capture.mp4")
    subprocess.run(
        ["ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "x11grab",
         "-r", "60", "-s", f"{SCREEN_W}x{SCREEN_H}", "-i", DISPLAY, "-t", str(seconds),
         "-c:v", "hevc_nvenc", "-preset", "p4", "-b:v", "4M", out],
        check=False, env={**os.environ, "DISPLAY": DISPLAY},
    )
    try:
        return os.path.getsize(out)
    except OSError:
        return 0


def spectate(target_name: str) -> int:
    """Full validated path: clear modals -> (friends panel already open) -> locate the friend in the
    IN-DOTA list -> right-click -> WATCH FRIEND LIVE / WATCH GAME -> join (player view) -> NVENC
    capture. Returns the capture byte size (0 = failure)."""
    anchors, used_default = load_anchors()
    ctx = Context(anchors=anchors, using_default_anchors=used_default)
    try:
        raise_dota()            # bring Dota to front (a Steam chat popup can cover it)
        step_dashboard(ctx)
        step_open_friends(ctx)
        step_locate_and_spectate(ctx, target_name)
        step_join_wait(ctx)
    except SpectateAborted as ex:
        _log(f"ABORT: {ex}")
        return 0
    sz = capture()
    _log(f"capture bytes: {sz}")
    return sz


# ============================================== CLI ==============================================

def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    sub = ap.add_subparsers(dest="cmd", required=True)

    sp = sub.add_parser("spectate", help="run the full GUI spectate path + capture")
    sp.add_argument("--target-name", required=True, help="friend's persona name (from ListFriends)")

    cr = sub.add_parser("click-ratio", help="probe-click a screen ratio (calibration)")
    cr.add_argument("rx", type=float)
    cr.add_argument("ry", type=float)
    cr.add_argument("--button", choices=["left", "right"], default="left")

    oc = sub.add_parser("ocr", help="dump OCR text of a fresh still")
    oc.add_argument("--region", default=None, help="WxH+X+Y screen region")
    oc.add_argument("--psm", type=int, default=None)

    args = ap.parse_args()

    if args.cmd == "spectate":
        sz = spectate(args.target_name)
        if sz > 200000:
            print(f"V5 LIKELY-PASS: non-trivial render ({sz} bytes). Confirm it is the live match.")
            return 0
        print(f"V5 INCOMPLETE: capture {sz} bytes. Inspect {SHOTDIR} / {LOGDIR}.")
        return 1

    if args.cmd == "click-ratio":
        ctx = Context(anchors={})
        x, y = click_ratio(ctx, args.rx, args.ry, args.button)
        print(f"clicked {args.button} @ ratio ({args.rx},{args.ry}) -> {x},{y} "
              f"(screen {SCREEN_W}x{SCREEN_H})")
        return 0

    if args.cmd == "ocr":
        region = None
        if args.region:
            wh, _, xy = args.region.partition("+")
            w, _, h = wh.partition("x")
            x, _, y = xy.partition("+")
            region = (int(x), int(y), int(w), int(h))
        print(ocr_text("ocr-probe", region=region, psm=args.psm))
        return 0

    return 2


if __name__ == "__main__":
    raise SystemExit(main())

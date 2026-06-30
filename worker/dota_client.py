"""Dota 2 GUI automation: drive the dashboard -> live-spectate a friend (player view).

There is NO console command that joins a live match by id (`dota_spectate_game` does not exist in
this build). Live friend-spectating is GC-mediated through the GUI (validated live 2026-06-24, see
docs/validation-results.md V5): the friends panel is docked open on the LEFT with an "IN DOTA (n)"
list; right-click the friend in a live match -> a context menu opens -> click WATCH FRIEND LIVE (the
Dota+ low-latency option, preferred) or WATCH GAME (the standard delayed fallback). The native
client does the GC handshake / SDR ticket / connect / render; we only drive mouse + keyboard. WATCH
FRIEND LIVE lands directly in PLAYER VIEW (the friend's own camera) — the desired output — so there
is NO in-session camera-follow step.

This is the worker port of the validated harness automation
(scripts/validation/desktop/gui_spectate.py). The pure decision logic (modal classification, OCR
box parsing, fuzzy matching) is kept at module level and unit-tested in tests/test_dota_decisions.py;
the I/O lives on ``DotaClient``, which drives input through the same proven daemons the harness uses
(it writes ``Pointer …`` lines to vnc_input_daemon's FIFO and console/RAW keys to uinput_daemon's
FIFO) rather than owning the kernel devices itself.

Device ownership stays with the standalone daemons because the devices must exist + be retagged
before Xorg's startup input enumeration binds them via libinput — an ordering a process that starts
after Xorg can't satisfy. The container (worker/deploy) creates the devices and gates Xorg on them;
this client just needs the FIFOs present. Order: daemons create devices -> Xorg binds -> GUI Steam
login -> ``launch_dota()`` -> ``spectate(name)``.
"""

from __future__ import annotations

import logging
import os
import subprocess
import time
from dataclasses import dataclass, field
from difflib import SequenceMatcher

log = logging.getLogger("worker.dota")

# The worker drives input through the same proven standalone daemons the harness uses (created
# before the Xorg re-enumeration so libinput binds them): the mouse via vnc_input_daemon's FIFO
# (write "Pointer <cnt> <x> <y> <mask>"), the keyboard/console via uinput_daemon's FIFO. The worker
# does NOT own the uinput devices — that ordering can't be satisfied from a process that starts
# after Xorg. See worker/deploy + scripts/validation/v5_spectate.sh (setup_uinput).
VNC_FIFO = os.environ.get("VNC_FIFO", "/tmp/vnc_input.fifo")
UINPUT_FIFO = os.environ.get("UINPUT_FIFO", "/tmp/dota_uinput.fifo")


# ============================ pure decision logic (unit-tested) ============================

# Catalogued OCR state signatures (anchor substrings, normalized). Order matters: a modal title is
# checked before the bare-dashboard PLAY DOTA so a modal sitting over the dashboard is caught.
MODAL_SIGNATURES = [
    ("UPDATE_REQUIRED", ["out of date", "update required"]),
    ("BEHAVIOR_SUMMARY", ["player behavior summary", "behavior summary"]),
    ("PARTY_INVITE", ["party invitation"]),
    ("DOTA_PLUS", ["welcome to dota plus", "dota plus"]),
]
DASHBOARD_SIGNATURE = ["play dota"]

# Context-menu item labels, in PREFERENCE order: WATCH FRIEND LIVE is the Dota+ low-latency option
# (use it when present); WATCH GAME is the standard delayed fallback shown for any live match.
SPECTATE_LABELS = ("Watch Friend Live", "Watch Game")


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


def panel_open(text: str) -> bool:
    """True if an OCR dump of the left strip shows the docked friends panel. Pure."""
    n = _norm(text)
    return "in dota" in n or "search friends" in n or "add friend" in n


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
        idx = {k: header.index(k) for k in ("left", "top", "width", "height", "conf", "text")}
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
            left = int(cols[idx["left"]])
            top = int(cols[idx["top"]])
            w = int(cols[idx["width"]])
            h = int(cols[idx["height"]])
            conf = float(cols[idx["conf"]])
        except ValueError:
            continue
        if conf < 0:  # tesseract emits -1 for non-word rows
            continue
        sl = region_x + int(left / scale)
        st = region_y + int(top / scale)
        sw = int(w / scale)
        sh = int(h / scale)
        boxes.append(Box(text, sl + sw // 2, st + sh // 2, sl, st, sw, sh, conf))
    return boxes


def _merge_boxes(window: list[Box]) -> Box:
    left = min(b.left for b in window)
    top = min(b.top for b in window)
    right = max(b.left + b.width for b in window)
    bottom = max(b.top + b.height for b in window)
    conf = min(b.conf for b in window)
    text = " ".join(b.text for b in window)
    return Box(text, (left + right) // 2, (top + bottom) // 2,
               left, top, right - left, bottom - top, conf)


def find_text_box(boxes: list[Box], target: str, min_conf: float = 40.0) -> Box | None:
    """Best fuzzy match for `target` among word boxes. Pure. Returns the box (or merged box) to
    click, or None.

    Tries, in order: (1) the full multi-word window, then (2) a single DISTINCTIVE target word
    (len>=4) against any box. The single-word fallback matters because the stylized Dota font mangles
    part of a persona badly (validated 2026-06-24: "zitraks mops" OCR'd "ritraks mops" — the first
    word is wrecked but "mops" reads clean), and clicking anywhere on the friend's row works.
    """
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


# ===================================== I/O: DotaClient =====================================

# Icon-only chrome that OCR can't see, as (rx, ry) fractions of the screen. APPROXIMATE — overwrite
# via a calib file. Loudly warned about at runtime so an uncalibrated run can never silently misclick.
DEFAULT_ANCHORS = {
    "behavior_close": (0.745, 0.205),   # Player Behavior Summary close-X (ESC/ENTER don't work)
    "dota_plus_close": (0.745, 0.205),  # Welcome-to-Dota-Plus dismiss
    "update_ok": (0.498, 0.565),        # "Update Required" OK button (validated live 2026-06-24)
}

_MOVE_STEPS = 15
_MOVE_STEP_DELAY = 0.03
JOIN_SETTLE_SECONDS = 12  # asset/map load after the dashboard tears down, before declaring success


class SpectateError(Exception):
    """A spectate step failed. ``code`` maps to the worker ErrorEvent code (FRIEND_NOT_FOUND,
    SPECTATE_FAILED)."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code


@dataclass
class DotaConfig:
    display: str = field(default_factory=lambda: os.environ.get("DISPLAY", ":99"))
    shotdir: str = field(
        default_factory=lambda: os.environ.get("SHOTDIR", "/fard/steam/steamhome/v5-shots")
    )
    dota_dir: str = field(default_factory=lambda: os.environ.get("DOTA_DIR", "/fard/steam/dota"))
    home: str = field(default_factory=lambda: os.environ.get("HOME", "/fard/steam/steamhome"))
    screen_w: int = 0
    screen_h: int = 0

    def __post_init__(self) -> None:
        if not self.screen_w or not self.screen_h:
            w, _, h = os.environ.get("SCREEN", "1280x720").partition("x")
            self.screen_w = self.screen_w or int(w or 1280)
            self.screen_h = self.screen_h or int(h or 720)


class DotaClient:
    """Drives the OCR-gated spectate path through the proven uinput daemons (via FIFOs).

    Pure decision logic is the module-level functions above; this class is the I/O seam (FIFO input,
    screenshots/OCR via subprocess, the step machine). It is NOT unit-tested — it's validated live
    in the harness (gui_spectate.py); tests cover the pure logic it delegates to.
    """

    def __init__(self, config: DotaConfig | None = None, anchors: dict | None = None) -> None:
        self.cfg = config or DotaConfig()
        self.anchors = {**DEFAULT_ANCHORS, **(anchors or {})}
        self._env = {**os.environ, "DISPLAY": self.cfg.display}
        self._counter = 0  # monotonic Pointer sequence id the vnc bridge expects
        self._pos = [self.cfg.screen_w // 2, self.cfg.screen_h // 2]

    def setup(self) -> None:
        """Verify the input FIFOs the daemons own are present. The devices themselves are created by
        the standalone uinput/vnc daemons BEFORE the Xorg re-enumeration (see worker/deploy), since a
        process starting after Xorg can't get its devices bound. Best-effort: warns, never raises."""
        for fifo in (VNC_FIFO, UINPUT_FIFO):
            if not os.path.exists(fifo):
                log.warning("input FIFO %s missing — is the input daemon running? "
                            "mouse/keyboard will not reach Dota", fifo)

    def close(self) -> None:
        """No-op: the daemons own the devices/FIFOs, not this client."""

    # --- launch ---

    def launch_dota(self) -> None:
        """Launch Dota directly through the install's own sniper wrapper (run-in-sniper ->
        _v2-entry-point), with the GUI Steam client running only for auth. Ported from
        v5_spectate.sh launch_dota; the worker runs as the `worker` user inside the container, so no
        docker exec wrapping. The input daemons must already be running (devices bound to libinput)."""
        cfg = self.cfg
        cfgdir = os.path.join(cfg.dota_dir, "game", "dota", "cfg")
        os.makedirs(cfgdir, exist_ok=True)
        # con_enable + grave->toggleconsole so the keyboard can open the engine console if needed.
        with open(os.path.join(cfgdir, "autoexec.cfg"), "w") as f:
            f.write('con_enable 1\nbind "`" "toggleconsole"\n')
        env = {
            **self._env,
            "HOME": cfg.home,
            "XDG_RUNTIME_DIR": os.environ.get("XDG_RUNTIME_DIR", "/tmp/xdg-worker"),
            "SteamAppId": "570",
            "SteamGameId": "570",
        }
        log.info("launching Dota via run-in-sniper")
        subprocess.Popen(
            [
                "./run-in-sniper", "--",
                os.path.join(cfg.dota_dir, "game", "dota.sh"),
                "-novid", "-console", "-condebug", "-nosound", "-nopreload",
                "+developer", "1", "+exec", "autoexec",
            ],
            cwd=cfg.dota_dir,
            env=env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.STDOUT,
        )

    def _dota_window_present(self) -> bool:
        r = subprocess.run(
            ["bash", "-lc", "xdotool search --class dota2 >/dev/null 2>&1"],
            check=False, env=self._env,
        )
        return r.returncode == 0

    def wait_for_dota_window(self, timeout_seconds: float = 180.0,
                             poll_seconds: float = 5.0) -> None:
        """Block until the Dota window appears on :99 after launch_dota(). The sniper chain
        (srt-bwrap -> pv-adverb -> dota.sh) plus the Vulkan pipeline compile takes ~tens of seconds,
        so the timeout is generous. Raises SpectateError(DOTA_LAUNCH_FAILED) on timeout."""
        deadline = time.time() + timeout_seconds
        while time.time() < deadline:
            if self._dota_window_present():
                log.info("Dota window present")
                return
            time.sleep(poll_seconds)
        raise SpectateError("DOTA_LAUNCH_FAILED", "Dota window never appeared after launch")

    # --- screenshots + OCR (subprocess; same preprocess as the harness ocr.sh) ---

    def _screenshot(self, name: str) -> str:
        os.makedirs(self.cfg.shotdir, exist_ok=True)
        path = os.path.join(self.cfg.shotdir, f"{name}.png")
        subprocess.run(
            ["ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
             "-f", "x11grab", "-i", self.cfg.display, "-frames:v", "1", path],
            check=False, env=self._env,
        )
        return path

    def _convert_tesseract(self, img: str, region: str | None, psm: int, tsv: bool,
                           invert: bool = False, scale: float = 2.0) -> str:
        crop = ["-crop", region, "+repage"] if region else []
        neg = ["-negate"] if invert else []
        resize = f"{int(scale * 100)}%"
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

    def _ocr_text(self, name: str, region: tuple[int, int, int, int] | None = None,
                  psm: int | None = None) -> str:
        img = self._screenshot(name)
        reg = f"{region[2]}x{region[3]}+{region[0]}+{region[1]}" if region else None
        p = psm if psm is not None else (6 if region else 11)
        return self._convert_tesseract(img, reg, p, tsv=False)

    def _ocr_boxes(self, name: str, region: tuple[int, int, int, int] | None = None, psm: int = 6,
                   invert: bool = False, scale: float = 2.0) -> list[Box]:
        img = self._screenshot(name)
        rx, ry = (region[0], region[1]) if region else (0, 0)
        reg = f"{region[2]}x{region[3]}+{region[0]}+{region[1]}" if region else None
        tsv = self._convert_tesseract(img, reg, psm, tsv=True, invert=invert, scale=scale)
        return parse_tsv(tsv, region_x=rx, region_y=ry, scale=scale)

    # --- mouse (writes to vnc_input_daemon's FIFO; it owns dota-vnc-mouse) ---

    @staticmethod
    def _fifo_write(path: str, line: str) -> None:
        # The daemon reopens the FIFO per writer, so open/write/close per line is the contract.
        with open(path, "w") as f:
            f.write(line + "\n")

    def _pointer(self, x: int, y: int, mask: int) -> None:
        # Absolute coords + a button mask (bit0 left, bit2 right, bit3/4 wheel up/down). The daemon
        # scales to ABS range and computes button/wheel edges from its own last mask.
        self._counter += 1
        x = max(0, min(self.cfg.screen_w - 1, x))
        y = max(0, min(self.cfg.screen_h - 1, y))
        self._fifo_write(VNC_FIFO, f"Pointer {self._counter} {x} {y} {mask}")

    def _move(self, x: int, y: int) -> None:
        # VALIDATED LIVE (2026-06-24): Panorama only registers hover / opens a context menu if the
        # pointer ARRIVES via continuous motion. A teleport-then-click reaches the X server but the
        # UI does not react, so always interpolate a dense path from the last position.
        x = max(0, min(self.cfg.screen_w - 1, x))
        y = max(0, min(self.cfg.screen_h - 1, y))
        x0, y0 = self._pos
        for i in range(1, _MOVE_STEPS + 1):
            ix = round(x0 + (x - x0) * i / _MOVE_STEPS)
            iy = round(y0 + (y - y0) * i / _MOVE_STEPS)
            self._pointer(ix, iy, 0)
            time.sleep(_MOVE_STEP_DELAY)
        self._pos[0], self._pos[1] = x, y
        time.sleep(0.3)  # let the hover settle before any click

    def _click(self, x: int, y: int, button: str = "left") -> None:
        mask = 1 if button == "left" else 4  # bit0 left, bit2 right
        self._move(x, y)  # dense motion — required for the click to register
        self._pointer(x, y, mask)
        time.sleep(0.12)
        self._pointer(x, y, 0)
        time.sleep(0.6)
        log.info("%s-click @ %d,%d", button, x, y)

    def _click_anchor(self, name: str, button: str = "left") -> None:
        rx, ry = self.anchors[name]
        self._click(int(rx * self.cfg.screen_w), int(ry * self.cfg.screen_h), button)

    def _scroll_to_top(self) -> None:
        """Wheel the friends list UP so the IN-DOTA section (always at the top) is in view. Never
        scroll down: in-match friends sit at the top, so a down-scroll hides them (validated)."""
        region = self._friends_region()
        cx = region[0] + region[2] // 2
        self._move(cx, self.cfg.screen_h // 2)
        for _ in range(8):  # wheel-up edges (bit3) to reach the top
            self._pointer(cx, self.cfg.screen_h // 2, 1 << 3)
            self._pointer(cx, self.cfg.screen_h // 2, 0)
            time.sleep(0.05)
        time.sleep(0.6)

    # --- keyboard / console (writes to uinput_daemon's FIFO; it owns dota-spectate-uinput) ---

    def _tap_esc(self) -> None:
        # "RAW <KEY_NAME>" taps the evdev key directly (uinput_daemon contract).
        self._focus_dota()
        self._fifo_write(UINPUT_FIFO, "RAW KEY_ESC")
        time.sleep(2.0)  # daemon paces raw taps; let the dismiss register

    def _console(self, cmd: str) -> None:
        # A plain line opens the engine console, types, ENTER, closes (uinput_daemon contract).
        self._focus_dota()
        self._fifo_write(UINPUT_FIFO, cmd)
        time.sleep(4.0)

    def _focus_dota(self) -> None:
        subprocess.run(
            ["bash", "-lc",
             'WIN=$(xdotool search --class dota2 | head -1); '
             '[ -n "$WIN" ] && xdotool windowactivate --sync "$WIN" 2>/dev/null || true'],
            check=False, env=self._env,
        )

    def _raise_dota(self) -> None:
        """Bring Dota to the front before driving the mouse: the GUI Steam client can pop a
        friend-chat window on top of Dota (validated 2026-06-24). Closes transient Steam chat popups
        but NOT the main Steam window (Dota needs it for auth) nor the xfce panels."""
        subprocess.run(
            ["bash", "-lc",
             'wmctrl -l | awk \'$2==0 && $4!="Dota" && $4!="Steam" {print $1}\' | '
             '  while read w; do wmctrl -ic "$w" 2>/dev/null || true; done; '
             'WIN=$(xdotool search --class dota2 | head -1); '
             '[ -n "$WIN" ] && { xdotool windowactivate --sync "$WIN"; xdotool windowraise "$WIN"; } '
             '2>/dev/null || true'],
            check=False, env=self._env,
        )
        time.sleep(1.0)

    # --- recovery hook (log-only; a vision model drops in later without touching the steps) ---

    def _on_unexpected(self, step: str, expected: str, ocr_dump: str) -> str:
        logdir = os.path.join(self.cfg.shotdir, "gui_spectate")
        os.makedirs(logdir, exist_ok=True)
        base = os.path.join(logdir, f"{time.strftime('%Y%m%d-%H%M%S')}-{step}")
        try:
            with open(base + ".txt", "w") as f:
                f.write(f"step={step}\nexpected={expected}\n\n--- OCR ---\n{ocr_dump}\n")
        except Exception as ex:  # noqa: BLE001
            log.warning("on_unexpected: failed to persist dump: %s", ex)
        log.warning("on_unexpected[%s] expected=%r; logged to %s.*; decision=ABORT",
                    step, expected, base)
        return "ABORT"

    # --- the OCR-gated step machine (player view; NO camera-follow) ---

    def _friends_region(self) -> tuple[int, int, int, int]:
        return (0, 0, int(0.22 * self.cfg.screen_w), self.cfg.screen_h)

    def _gate(self, step: str, expected: str, check, *, tries: int = 6, delay: float = 2.0):
        last_text = ""
        for attempt in range(1, tries + 1):
            last_text = self._ocr_text(f"gate-{step.lower()}-{attempt}")
            result = check(last_text)
            if result:
                log.info("%s: gate satisfied (attempt %d)", step, attempt)
                return result
            time.sleep(delay)
        if self._on_unexpected(step, expected, last_text) == "RETRY":
            return self._gate(step, expected, check, tries=2, delay=delay)
        raise SpectateError("SPECTATE_FAILED", f"{step}: gate {expected!r} never satisfied")

    def _dashboard_ready(self, name: str = "dash-check") -> bool:
        """Full-screen psm-11 OCR misses the stylized PLAY DOTA button (validated), so check two
        reliable signals: PLAY DOTA via a bottom-right corner crop (psm 7), or the friends panel."""
        w, h = self.cfg.screen_w, self.cfg.screen_h
        btn_region = (int(0.78 * w), int(0.88 * h), int(0.22 * w), int(0.12 * h))
        if "play dota" in _norm(self._ocr_text(name, region=btn_region, psm=7)):
            return True
        return panel_open(self._ocr_text(name + "-panel", region=self._friends_region()))

    def _step_dashboard(self) -> None:
        """DASHBOARD + CLEAR_MODALS: loop clearing known first-login modals until the bare dashboard
        (PLAY DOTA / friends panel, no known modal) is showing."""
        for _ in range(8):
            text = self._ocr_text("state")
            state = classify_state(text)
            log.info("state = %s", state)
            if state == "DASHBOARD":
                return
            if state == "BEHAVIOR_SUMMARY":
                self._click_anchor("behavior_close")  # ESC/ENTER do NOT dismiss this one
            elif state == "PARTY_INVITE":
                self._tap_esc()
            elif state == "DOTA_PLUS":
                self._click_anchor("dota_plus_close")
            elif state == "UPDATE_REQUIRED":
                # An out-of-date client CAN still spectate live matches, so this is non-fatal — just
                # click OK (validated live 2026-06-24). steamcmd keeps the install current in prod.
                self._click_anchor("update_ok")
            else:  # UNKNOWN to the full-screen classifier — confirm via the robust dashboard check
                if self._dashboard_ready():
                    log.info("dashboard ready (corner PLAY DOTA / friends panel)")
                    return
                if self._on_unexpected("CLEAR_MODALS", "PLAY DOTA / known modal", text) != "RETRY":
                    raise SpectateError("SPECTATE_FAILED", "unknown overlay at dashboard")
            time.sleep(1.5)
        self._gate("DASHBOARD", "PLAY DOTA / friends panel",
                   lambda _t: self._dashboard_ready("gate-dashboard") and "DASHBOARD")

    def _step_open_friends(self) -> None:
        """OPEN_FRIENDS: the panel is docked OPEN on the left by default on this build; the header
        OCRs unreliably from the busy strip, so this is detection-only (never clicks an uncalibrated
        button, never hard-aborts). LOCATE_FRIEND is the real gate."""
        if panel_open(self._ocr_text("friends-check", region=self._friends_region())):
            log.info("friends panel detected open (docked left)")
        else:
            log.info("friends-panel header not OCR'd (busy strip); proceeding — panel is docked "
                     "open by default, LOCATE_FRIEND will confirm")

    def _step_locate_and_spectate(self, target_name: str) -> None:
        """LOCATE_FRIEND + SPECTATE: find the target's row in the IN-DOTA list by OCR, right-click to
        open the context menu, then click WATCH FRIEND LIVE (preferred) or WATCH GAME."""
        region = self._friends_region()
        self._scroll_to_top()  # ensure the IN-DOTA friends are visible at top
        target_box: Box | None = None
        for attempt in range(1, 6):
            # friend-row OCR is flaky (stylized/unicode personas, small text); RE-OCR on a miss, do
            # NOT scroll down. psm 11 + 3x upscale reads the IN-DOTA rows (psm 6 / 2x miss them).
            boxes = self._ocr_boxes(f"friends-{attempt}", region=region, psm=11, scale=3.0)
            target_box = find_text_box(boxes, target_name)
            if target_box:
                break
            time.sleep(1.0)
        if not target_box:
            self._on_unexpected("LOCATE_FRIEND", f"friend row {target_name!r}",
                                self._ocr_text("friends-text", region=region))
            raise SpectateError("FRIEND_NOT_FOUND",
                                f"friend {target_name!r} not found in IN-DOTA list")
        log.info("target %r row @ %d,%d (conf %.1f)",
                 target_name, target_box.cx, target_box.cy, target_box.conf)

        self._click(target_box.cx, target_box.cy, button="right")
        time.sleep(2.0)  # let the context menu fully open before OCR (too-fast misses it)
        # The context menu's position is VARIABLE: sometimes over the LEFT panel, sometimes to its
        # RIGHT and lower. Scope OCR to a WIDE region spanning both; light-grey on near-black + small
        # font -> invert + 3x upscale. The WATCH labels are distinctive multi-word phrases.
        w, h = self.cfg.screen_w, self.cfg.screen_h
        menu_region = (0, int(0.10 * h), int(0.45 * w), int(0.85 * h))

        def find_spectate(_t: str):
            boxes = self._ocr_boxes("menu", region=menu_region, invert=True, scale=3.0)
            for label in SPECTATE_LABELS:
                b = find_text_box(boxes, label)
                if b:
                    return b
            return None

        item = self._gate("SPECTATE", "WATCH FRIEND LIVE / WATCH GAME menu item",
                          find_spectate, tries=5, delay=1.2)
        self._click(item.cx, item.cy, button="left")
        log.info("clicked spectate item %r @ %d,%d", item.text, item.cx, item.cy)

    def _step_join_wait(self) -> None:
        """JOIN_WAIT: clicking WATCH FRIEND LIVE tears the dashboard down to a loading screen, then
        the live match renders in PLAYER VIEW (the desired output — no camera-follow). Gate on the
        dashboard being GONE, then settle for the map/asset load."""
        def left_dashboard(t: str):
            return ("play dota" not in _norm(t)) and True

        self._gate("JOIN_WAIT", "dashboard torn down (entering spectate)",
                   left_dashboard, tries=20, delay=3.0)
        log.info("left dashboard; settling %ds for the live match to load", JOIN_SETTLE_SECONDS)
        time.sleep(JOIN_SETTLE_SECONDS)

    def spectate(self, target_name: str) -> None:
        """Drive the validated GUI path from the dashboard to a live PLAYER-VIEW spectate of
        ``target_name`` (the friend's persona name from ListFriends). Assumes Dota is running at the
        dashboard and the input devices exist (setup() called before launch). Raises SpectateError
        (code FRIEND_NOT_FOUND / SPECTATE_FAILED) on a failed gate; the caller maps it to ErrorEvent.

        Returns on success once the match is rendering — the FFmpeg/StreamStarted leg (step 9) is the
        caller's next step.
        """
        if not target_name:
            raise SpectateError("FRIEND_NOT_FOUND", "no target persona name to locate")
        self.setup()  # idempotent; in production setup() ran pre-launch so this is a no-op
        self._raise_dota()  # a Steam chat popup can cover Dota
        self._step_dashboard()
        self._step_open_friends()
        self._step_locate_and_spectate(target_name)
        self._step_join_wait()

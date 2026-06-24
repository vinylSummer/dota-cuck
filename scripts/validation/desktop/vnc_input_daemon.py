#!/usr/bin/env python3
"""Route VNC input into a headless Source 2 (Dota 2) session via real kernel uinput devices.

Source 2 rejects XTEST synthetic events, which is exactly what a stock x11vnc injects — so a
naive VNC session can *view* Dota but not *control* it. This daemon is the bridge: x11vnc runs
with `-pipeinput "reopen,/usr/local/bin/vnc_pipe_forward.sh"`, the forwarder appends x11vnc's
input-event text stream to a FIFO, and this daemon translates those lines into events on two
persistent uinput devices that libinput claims as real input (so Dota accepts them):

  - "dota-vnc-mouse" — ABSOLUTE pointer (ABS_X/ABS_Y + INPUT_PROP_POINTER, BTN_LEFT/RIGHT/MIDDLE,
    REL_WHEEL). Absolute coords mean libinput maps the cursor 1:1 with no acceleration math, and
    the cursor tracks on hover (the QEMU/VirtualBox "virtual tablet" pattern).
  - "dota-vnc-kbd" — keyboard, mapping X keysym NAMES (which already encode shift, e.g. "A" vs
    "a", "underscore" vs "minus") to evdev keycodes.

The devices are created BEFORE Dota launches and OWNED BY THIS DAEMON, not x11vnc — so x11vnc can
restart freely (its forwarder just reconnects to the FIFO) without ever re-creating the devices,
which would break the one-shot "restart Xorg so libinput enumerates them" binding in
v5_spectate.sh setup_uinput().

x11vnc pipeinput line format (verified against x11vnc's pipe_pointer/pipe_keyboard):
    Pointer <cnt> <x> <y> <buttonmask>
    Keysym  <cnt> <down> <keysym_decimal> <KeySymName> <KeyPress|KeyRelease>
The first RAW_LOG_LINES are echoed to stderr so the exact format is confirmed live before relying
on it.

Usage: vnc_input_daemon.py [FIFO_PATH]   (default /tmp/vnc_input.fifo)
Env:   VNC_SCREEN=WxH  (framebuffer size for ABS scaling; default 1280x720)
Requires: /dev/uinput (--device /dev/uinput) + python3-evdev.
"""
import os
import stat
import sys
import time

from evdev import UInput, AbsInfo, ecodes as e

FIFO = sys.argv[1] if len(sys.argv) > 1 else "/tmp/vnc_input.fifo"
ABS_MAX = 32767
RAW_LOG_LINES = 20

_w, _, _h = os.environ.get("VNC_SCREEN", "1280x720").partition("x")
SCREEN_W = int(_w or 1280)
SCREEN_H = int(_h or 720)


def _log(msg):
    sys.stderr.write(f"[vnc_input_daemon] {msg}\n")
    sys.stderr.flush()


# --- keysym-name -> (evdev keycode, needs_shift) -----------------------------------------------
KEYMAP = {}
for _c in "abcdefghijklmnopqrstuvwxyz":
    kc = getattr(e, f"KEY_{_c.upper()}")
    KEYMAP[_c] = (kc, False)            # "a"
    KEYMAP[_c.upper()] = (kc, True)     # "A"
for _d in "0123456789":
    KEYMAP[_d] = (getattr(e, f"KEY_{_d}"), False)

# name -> (keycode, needs_shift). Shifted/unshifted symbol names map to the same physical key.
_SYMS = {
    "space": (e.KEY_SPACE, False),
    "Return": (e.KEY_ENTER, False),
    "BackSpace": (e.KEY_BACKSPACE, False),
    "Tab": (e.KEY_TAB, False),
    "Escape": (e.KEY_ESC, False),
    "grave": (e.KEY_GRAVE, False), "asciitilde": (e.KEY_GRAVE, True),
    "minus": (e.KEY_MINUS, False), "underscore": (e.KEY_MINUS, True),
    "equal": (e.KEY_EQUAL, False), "plus": (e.KEY_EQUAL, True),
    "bracketleft": (e.KEY_LEFTBRACE, False), "braceleft": (e.KEY_LEFTBRACE, True),
    "bracketright": (e.KEY_RIGHTBRACE, False), "braceright": (e.KEY_RIGHTBRACE, True),
    "backslash": (e.KEY_BACKSLASH, False), "bar": (e.KEY_BACKSLASH, True),
    "semicolon": (e.KEY_SEMICOLON, False), "colon": (e.KEY_SEMICOLON, True),
    "apostrophe": (e.KEY_APOSTROPHE, False), "quotedbl": (e.KEY_APOSTROPHE, True),
    "comma": (e.KEY_COMMA, False), "less": (e.KEY_COMMA, True),
    "period": (e.KEY_DOT, False), "greater": (e.KEY_DOT, True),
    "slash": (e.KEY_SLASH, False), "question": (e.KEY_SLASH, True),
    "exclam": (e.KEY_1, True), "at": (e.KEY_2, True), "numbersign": (e.KEY_3, True),
    "dollar": (e.KEY_4, True), "percent": (e.KEY_5, True), "asciicircum": (e.KEY_6, True),
    "ampersand": (e.KEY_7, True), "asterisk": (e.KEY_8, True),
    "parenleft": (e.KEY_9, True), "parenright": (e.KEY_0, True),
    "Left": (e.KEY_LEFT, False), "Right": (e.KEY_RIGHT, False),
    "Up": (e.KEY_UP, False), "Down": (e.KEY_DOWN, False),
    "Home": (e.KEY_HOME, False), "End": (e.KEY_END, False),
    "Prior": (e.KEY_PAGEUP, False), "Next": (e.KEY_PAGEDOWN, False),
    "Insert": (e.KEY_INSERT, False), "Delete": (e.KEY_DELETE, False),
}
for _n in range(1, 13):
    _SYMS[f"F{_n}"] = (getattr(e, f"KEY_F{_n}"), False)
KEYMAP.update(_SYMS)

# Modifier keysym names tracked + passed straight through (for game combos like Ctrl/Alt-held).
MOD_MAP = {
    "Shift_L": e.KEY_LEFTSHIFT, "Shift_R": e.KEY_RIGHTSHIFT,
    "Control_L": e.KEY_LEFTCTRL, "Control_R": e.KEY_RIGHTCTRL,
    "Alt_L": e.KEY_LEFTALT, "Alt_R": e.KEY_RIGHTALT,
    "Meta_L": e.KEY_LEFTMETA, "Meta_R": e.KEY_RIGHTMETA,
    "Super_L": e.KEY_LEFTMETA, "Super_R": e.KEY_RIGHTMETA,
    "Caps_Lock": e.KEY_CAPSLOCK,
}

KBD_KEYS = sorted({kc for kc, _ in KEYMAP.values()} | set(MOD_MAP.values()))

# VNC button mask bits -> pointer button keycodes (bit3/bit4 are wheel up/down).
BTN_BITS = {0: e.BTN_LEFT, 1: e.BTN_MIDDLE, 2: e.BTN_RIGHT}


class Bridge:
    def __init__(self):
        self.kbd = UInput({e.EV_KEY: KBD_KEYS}, name="dota-vnc-kbd")
        self.mouse = UInput(
            {
                e.EV_KEY: [e.BTN_LEFT, e.BTN_RIGHT, e.BTN_MIDDLE],
                e.EV_ABS: [
                    (e.ABS_X, AbsInfo(0, 0, ABS_MAX, 0, 0, 0)),
                    (e.ABS_Y, AbsInfo(0, 0, ABS_MAX, 0, 0, 0)),
                ],
                e.EV_REL: [e.REL_WHEEL],
            },
            name="dota-vnc-mouse",
            input_props=[e.INPUT_PROP_POINTER],
        )
        self.held_mods = set()
        self.temp_shift = {}   # keycode -> we injected a momentary shift for it
        self.last_mask = 0
        time.sleep(0.6)        # let the devices register before anyone enumerates them
        _log(f"devices created: dota-vnc-kbd, dota-vnc-mouse (screen {SCREEN_W}x{SCREEN_H})")

    # --- pointer ---------------------------------------------------------------------------
    def pointer(self, x, y, mask):
        ax = max(0, min(ABS_MAX, round(x * ABS_MAX / max(1, SCREEN_W - 1))))
        ay = max(0, min(ABS_MAX, round(y * ABS_MAX / max(1, SCREEN_H - 1))))
        self.mouse.write(e.EV_ABS, e.ABS_X, ax)
        self.mouse.write(e.EV_ABS, e.ABS_Y, ay)
        changed = mask ^ self.last_mask
        for bit, btn in BTN_BITS.items():
            if changed & (1 << bit):
                self.mouse.write(e.EV_KEY, btn, 1 if mask & (1 << bit) else 0)
        for bit, delta in ((3, 1), (4, -1)):           # wheel up / down on press edge
            if (changed & (1 << bit)) and (mask & (1 << bit)):
                self.mouse.write(e.EV_REL, e.REL_WHEEL, delta)
        self.last_mask = mask
        self.mouse.syn()

    # --- keyboard --------------------------------------------------------------------------
    def keysym(self, down, name):
        if name in MOD_MAP:
            kc = MOD_MAP[name]
            self.kbd.write(e.EV_KEY, kc, 1 if down else 0)
            (self.held_mods.add if down else self.held_mods.discard)(kc)
            self.kbd.syn()
            return
        entry = KEYMAP.get(name)
        if entry is None:
            _log(f"unmapped keysym name {name!r} (down={down})")
            return
        kc, shift_req = entry
        if down:
            need_temp = shift_req and not (
                e.KEY_LEFTSHIFT in self.held_mods or e.KEY_RIGHTSHIFT in self.held_mods
            )
            if need_temp:
                self.kbd.write(e.EV_KEY, e.KEY_LEFTSHIFT, 1)
                self.temp_shift[kc] = True
            self.kbd.write(e.EV_KEY, kc, 1)
        else:
            self.kbd.write(e.EV_KEY, kc, 0)
            if self.temp_shift.pop(kc, False):
                self.kbd.write(e.EV_KEY, e.KEY_LEFTSHIFT, 0)
        self.kbd.syn()


def main():
    # x11vnc's `-pipeinput reopen` forwarder may have raced ahead and created a REGULAR file at
    # FIFO (bash `>>` on a missing path makes a plain file); replace any non-FIFO with a real FIFO.
    try:
        if not stat.S_ISFIFO(os.stat(FIFO).st_mode):
            os.unlink(FIFO)
    except FileNotFoundError:
        pass
    try:
        os.mkfifo(FIFO)
    except FileExistsError:
        pass
    os.chmod(FIFO, 0o666)
    br = Bridge()
    _log(f"ready; reading {FIFO}")
    raw_seen = 0
    while True:
        with open(FIFO) as f:                  # blocks until a writer (the x11vnc forwarder) opens
            for line in f:
                line = line.strip()
                if not line:
                    continue
                if raw_seen < RAW_LOG_LINES:
                    _log(f"RAW: {line}")
                    raw_seen += 1
                tok = line.split()
                try:
                    if tok[0] == "Pointer" and len(tok) >= 5:
                        br.pointer(int(tok[2]), int(tok[3]), int(tok[4]))
                    elif tok[0] == "Keysym" and len(tok) >= 5:
                        br.keysym(int(tok[2]) != 0, tok[4])
                except Exception as ex:           # never let one bad line kill the bridge
                    _log(f"parse error on {line!r}: {ex}")


if __name__ == "__main__":
    main()

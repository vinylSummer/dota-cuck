#!/usr/bin/env python3
"""Drive the Dota 2 (Source 2) in-game console via a virtual kernel input device.

Source 2 reads raw evdev devices and ignores XTEST synthetic events, so xdotool can't
type into the console. This creates a uinput keyboard (the same mechanism ydotool uses),
which the game picks up from /dev/input, then sends: GRAVE (open console) → the command
text → ENTER → GRAVE (close console).

Usage: uinput_console.py "<console command>"
Requires: /dev/uinput passed into the container (--device /dev/uinput) + python3-evdev.
The Dota window should already hold X focus (xdotool windowactivate) before this runs.
"""
import sys
import time

from evdev import UInput, ecodes as e

# Char → (keycode, needs_shift) for everything the V5 spectate commands use.
_KEYMAP = {}
for _c in "abcdefghijklmnopqrstuvwxyz":
    _KEYMAP[_c] = (getattr(e, f"KEY_{_c.upper()}"), False)
for _d in "0123456789":
    _KEYMAP[_d] = (getattr(e, f"KEY_{_d}"), False)
_KEYMAP[" "] = (e.KEY_SPACE, False)
_KEYMAP["_"] = (e.KEY_MINUS, True)   # underscore = shift + '-'
_KEYMAP["-"] = (e.KEY_MINUS, False)
_KEYMAP["."] = (e.KEY_DOT, False)


def _tap(ui, keycode, shift=False):
    if shift:
        ui.write(e.EV_KEY, e.KEY_LEFTSHIFT, 1)
        ui.syn()
    ui.write(e.EV_KEY, keycode, 1)
    ui.syn()
    ui.write(e.EV_KEY, keycode, 0)
    ui.syn()
    if shift:
        ui.write(e.EV_KEY, e.KEY_LEFTSHIFT, 0)
        ui.syn()
    time.sleep(0.02)


def main():
    if len(sys.argv) < 2:
        sys.exit("usage: uinput_console.py '<console command>'")
    cmd = sys.argv[1]

    unknown = sorted({c for c in cmd if c not in _KEYMAP})
    if unknown:
        sys.exit(f"unmapped characters in command: {unknown!r}")

    # Advertise every key we might press so the device is created with the right caps.
    caps = {e.EV_KEY: sorted({e.KEY_GRAVE, e.KEY_ENTER, e.KEY_LEFTSHIFT}
                             | {kc for kc, _ in _KEYMAP.values()})}
    with UInput(caps, name="dota-spectate-uinput") as ui:
        time.sleep(0.6)          # let udev register the device so the game enumerates it
        _tap(ui, e.KEY_GRAVE)    # open console
        time.sleep(0.8)
        for ch in cmd:
            kc, shift = _KEYMAP[ch]
            _tap(ui, kc, shift)
        time.sleep(0.3)
        _tap(ui, e.KEY_ENTER)    # submit
        time.sleep(0.8)
        _tap(ui, e.KEY_GRAVE)    # close console
        time.sleep(0.3)
    print(f"sent via uinput: {cmd}")


if __name__ == "__main__":
    main()

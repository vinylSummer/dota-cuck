#!/usr/bin/env python3
"""Persistent virtual keyboard for driving the Dota 2 (Source 2) in-game console.

Source 2 ignores XTEST synthetic events, so input must come from a real kernel input
device (/dev/uinput). Crucially, the game enumerates input devices at startup and — in a
container with no running udev daemon — does NOT pick up hotplugged devices. So the virtual
keyboard must exist BEFORE Dota launches and stay alive for the whole session.

This daemon creates one persistent UInput device, then reads commands from a FIFO:
  - a normal line is typed into the console: GRAVE (open) -> text -> ENTER -> GRAVE (close)
  - "RAW <KEY_NAME> [KEY_NAME ...]" taps those evdev keys directly (e.g. RAW KEY_ESC)
  - "__QUIT__" exits

Usage: uinput_daemon.py [FIFO_PATH]   (default /tmp/dota_uinput.fifo)
Requires: /dev/uinput in the container (--device /dev/uinput) + python3-evdev.
"""
import os
import sys
import time

from evdev import UInput, ecodes as e

FIFO = sys.argv[1] if len(sys.argv) > 1 else "/tmp/dota_uinput.fifo"

_KEYMAP = {}
for _c in "abcdefghijklmnopqrstuvwxyz":
    _KEYMAP[_c] = (getattr(e, f"KEY_{_c.upper()}"), False)
for _d in "0123456789":
    _KEYMAP[_d] = (getattr(e, f"KEY_{_d}"), False)
_KEYMAP[" "] = (e.KEY_SPACE, False)
_KEYMAP["_"] = (e.KEY_MINUS, True)
_KEYMAP["-"] = (e.KEY_MINUS, False)
_KEYMAP["."] = (e.KEY_DOT, False)


def _log(msg):
    sys.stderr.write(f"[uinput_daemon] {msg}\n")
    sys.stderr.flush()


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
    time.sleep(0.03)


def _type_console(ui, cmd):
    unknown = sorted({c for c in cmd if c not in _KEYMAP})
    if unknown:
        _log(f"skip: unmapped chars {unknown!r} in {cmd!r}")
        return
    _tap(ui, e.KEY_GRAVE)
    # Dota's Panorama console takes ~2-3s to give its input field keyboard focus after the
    # overlay opens; typing sooner is swallowed by the dashboard behind it (verified headless).
    time.sleep(3.0)
    for _ in range(48):  # clear any leftover/autocomplete text in the input field
        _tap(ui, e.KEY_BACKSPACE)
    for ch in cmd:
        kc, shift = _KEYMAP[ch]
        _tap(ui, kc, shift)
    time.sleep(0.3)
    _tap(ui, e.KEY_ENTER)
    time.sleep(0.8)
    _tap(ui, e.KEY_GRAVE)
    _log(f"typed: {cmd}")


def _raw(ui, names):
    for name in names:
        kc = getattr(e, name, None)
        if kc is None:
            _log(f"skip: unknown key {name}")
            continue
        _tap(ui, kc)
        time.sleep(0.2)
    _log(f"raw: {' '.join(names)}")


def main():
    try:
        os.mkfifo(FIFO)
    except FileExistsError:
        pass

    caps = {e.EV_KEY: sorted({e.KEY_GRAVE, e.KEY_ENTER, e.KEY_LEFTSHIFT, e.KEY_ESC,
                              e.KEY_SPACE, e.KEY_TAB, e.KEY_BACKSPACE,
                              e.KEY_F1, e.KEY_F2, e.KEY_F3, e.KEY_F4, e.KEY_F5, e.KEY_F6,
                              e.KEY_F7, e.KEY_F8, e.KEY_F9, e.KEY_F10, e.KEY_F11, e.KEY_F12}
                             | {kc for kc, _ in _KEYMAP.values()})}
    with UInput(caps, name="dota-spectate-uinput") as ui:
        time.sleep(0.6)  # let the device register before any consumer enumerates it
        _log(f"ready (device created); reading {FIFO}")
        while True:
            with open(FIFO) as f:  # blocks until a writer opens + sends
                for line in f:
                    cmd = line.strip()
                    if not cmd:
                        continue
                    if cmd == "__QUIT__":
                        _log("quit")
                        return
                    if cmd.startswith("RAW "):
                        _raw(ui, cmd.split()[1:])
                    else:
                        _type_console(ui, cmd)


if __name__ == "__main__":
    main()

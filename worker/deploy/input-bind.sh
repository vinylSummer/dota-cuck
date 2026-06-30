#!/usr/bin/env bash
# One-shot: make the worker's 3 uinput devices usable by libinput, then signal
# that Xorg may start. The input daemons (uinput_daemon, vnc_input_daemon) create
# the devices; here we ensure their /dev/input nodes exist and retag the udev db
# so libinput accepts them (dumb-udev mis-tags virtual devices as JOYSTICK, which
# libinput refuses). Ported verbatim from the proven scripts/validation/
# v5_spectate.sh setup_uinput. Runs as root (mknod + /run/udev writes).
set -u
UINPUT_NAMES="dota-spectate-uinput dota-vnc-kbd dota-vnc-mouse"

chmod 666 /dev/uinput 2>/dev/null || true

# Wait for the daemons to register all 3 devices in /sys.
for name in $UINPUT_NAMES; do
    for _ in $(seq 1 30); do
        grep -lqs "$name" /sys/class/input/*/device/name 2>/dev/null && break
        sleep 1
    done
done

# Ensure a /dev/input/eventN node exists for ONLY our named devices (CAP_MKNOD is
# granted by default); skip every other /sys input device.
for d in /sys/class/input/event*; do
    n=$(cat "$d/device/name" 2>/dev/null) || continue
    case " $UINPUT_NAMES " in *" $n "*) ;; *) continue ;; esac
    ev=$(basename "$d"); path="/dev/input/$ev"
    if [ ! -e "$path" ]; then
        IFS=: read -r major minor < "$d/dev"
        mknod "$path" c "$major" "$minor" 2>/dev/null || true
    fi
    chmod 0666 "$path" 2>/dev/null || true
done

# Retag the udev db entry to the correct ID_INPUT_* so libinput classifies + binds.
tag() { case "$1" in dota-vnc-mouse) echo MOUSE ;; *) echo KEYBOARD ;; esac; }
for d in /sys/class/input/event*; do
    n=$(cat "$d/device/name" 2>/dev/null) || continue
    case " $UINPUT_NAMES " in *" $n "*) ;; *) continue ;; esac
    t=$(tag "$n"); f="/run/udev/data/c$(cat "$d/dev")"
    if [ -f "$f" ]; then sed -i "s/ID_INPUT_JOYSTICK/ID_INPUT_$t/" "$f"; fi
    grep -q "ID_INPUT_$t" "$f" 2>/dev/null \
        || printf "E:ID_INPUT=1\nE:ID_INPUT_%s=1\nG:seat\nG:uaccess\n" "$t" > "$f"
    echo "input-bind: tagged $n as $t"
done

echo "input-bind: $UINPUT_NAMES ready; releasing Xorg"
touch /tmp/uinput-ready

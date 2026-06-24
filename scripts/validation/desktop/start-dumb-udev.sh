#!/usr/bin/env bash
# dumb-udev udev provider — adapted from docker-steam-headless's overlay/usr/bin/start-dumb-udev.sh
# for the V5 headless container (read-only /sys, no CAP_SYS_ADMIN, host /sys/class/input leaking
# through). This is the path that actually fixes the V5 spectate-console hang.
#
# Why it's needed: with no /run/udev, Source 2's libudev input enumeration busy-loops — dota2
# sits at ~216% CPU with no window, spinning on the udev-monitor netlink (recvmsg errors +
# an epoll_create1 storm; confirmed via strace). dumb-udev creates /run/udev{,/control,/data},
# watches the kernel netlink for VIRTUAL input adds, writes /run/udev/data/c<maj>:<min>, and
# re-broadcasts a libudev-format "udev" event so libudev sees the device and settles.
#
# dumb-udev only reacts to /sys/devices/virtual/input, so the host input devices that leak
# through the shared, read-only /sys (Power Button, HD-Audio jacks) are ignored — and the
# fallback node sync below is likewise restricted to OUR device, since exposing host nodes
# under /dev/input caused a separate enumeration hang earlier.
set -e

OUR_DEV_NAME="${UINPUT_DEVICE_NAME:-dota-spectate-uinput}"

# CATCH TERM SIGNAL:
_term() {
    kill -TERM "${sync_pid:-}" 2>/dev/null
    kill -TERM "${dumb_udev_pid:-}" 2>/dev/null
}
trap _term SIGTERM SIGINT

# Belt-and-suspenders node creation for ONLY our device, covering the case where it pre-dated
# dumb-udev (a restart): dumb-udev itself reacts only to live add events. Every other
# /sys/class/input entry is deliberately skipped.
sync_our_input_node() {
    mkdir -p /dev/input
    for d in /sys/class/input/event*; do
        [ "$(cat "$d/device/name" 2>/dev/null)" = "$OUR_DEV_NAME" ] || continue
        ev=$(basename "$d"); path="/dev/input/$ev"
        if [ -e "$path" ]; then chmod 0666 "$path" 2>/dev/null || true; continue; fi
        IFS=: read -r major minor < "$d/dev"
        mknod "$path" c "$major" "$minor" 2>/dev/null && chmod 0666 "$path" 2>/dev/null || true
    done
}

# Best-effort real udevd first (gives a genuine /run/udev/control); a harmless no-op when /sys
# is read-only / there is no CAP_SYS_ADMIN, which is our case — dumb-udev then owns /run/udev.
if command -v udevd &>/dev/null; then
    unshare --net udevd --daemon &>/dev/null || true
fi

dumb-udev &
dumb_udev_pid=$!

while true; do
    sync_our_input_node
    sleep 2
done &
sync_pid=$!

# WAIT FOR CHILD PROCESS:
wait "$dumb_udev_pid"

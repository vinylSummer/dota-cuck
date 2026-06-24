#!/usr/bin/env bash
# Real-udevd udev provider — mirrors docker-steam-headless's overlay/usr/bin/start-udev.sh.
# Used only when the environment supports a genuine udevd (writable /sys + /run/udev +
# a working `udevadm trigger`); start-udev-auto.sh makes that decision. Our V5 container
# runs /sys read-only with no CAP_SYS_ADMIN, so it normally falls back to dumb-udev instead.
set -e

# CATCH TERM SIGNAL:
_term() {
    kill -TERM "$monitor_pid" 2>/dev/null
}
trap _term SIGTERM SIGINT

# Start udev. `unshare --net` lets udevd come up even with a read-only /sys and populates
# /run/udev/control (what libudev needs to stop busy-looping on input enumeration).
if command -v udevd &>/dev/null; then
    unshare --net udevd --daemon &>/dev/null
else
    unshare --net /lib/systemd/systemd-udevd --daemon &>/dev/null
fi
# Monitor kernel uevents
udevadm monitor &
monitor_pid=$!
# Wait for 5 seconds, then request device events from the kernel
sleep 5
udevadm trigger

# WAIT FOR CHILD PROCESS:
wait "$monitor_pid"

#!/usr/bin/env bash
# Pick a udev provider and run it — the supervised entry point for the `udev` program.
# Folds docker-steam-headless's overlay/etc/cont-init.d/30-configure_udev.sh decision logic
# into one script (we have a flat supervisord.conf, not their cont-init.d/s6 layout).
#
# A real udevd needs a writable /sys, an existing+writable /run/udev, and a working
# `udevadm trigger`. Any miss => dumb-udev. The V5 container runs /sys read-only with no
# CAP_SYS_ADMIN, so this resolves to dumb-udev, which synthesizes /run/udev + /run/udev/data
# + libudev "udev" events for our virtual keyboard (the actual fix for the input hang).
set -e

# The persistent keyboard daemon runs as the worker user, so it must be able to open /dev/uinput.
[ -e /dev/uinput ] && chmod 0666 /dev/uinput 2>/dev/null || true

if [ -w /sys ] && [ -d /run/udev ] && [ -w /run/udev ] && udevadm trigger &>/dev/null; then
    echo "[udev] environment supports real udevd — starting udevd"
    exec /usr/local/bin/start-udev.sh
fi

echo "[udev] read-only /sys or insufficient privileges — starting dumb-udev"
exec /usr/local/bin/start-dumb-udev.sh

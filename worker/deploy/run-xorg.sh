#!/usr/bin/env bash
# Gate Xorg startup on the uinput devices being created + retagged (input-bind.sh
# touches /tmp/uinput-ready), so Xorg's one-time startup input enumeration binds
# them via libinput with NO restart needed.
set -u
for _ in $(seq 1 120); do
    [ -e /tmp/uinput-ready ] && break
    sleep 0.5
done
exec Xorg :99 -config /etc/X11/xorg.conf -noreset -ac

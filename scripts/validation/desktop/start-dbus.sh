#!/bin/bash
# System dbus bus. steamwebhelper's CEF spams "Failed to connect to /run/dbus/system_bus_socket"
# without it; docker-steam-headless runs a system bus too. Set up the machine-id + runtime dir,
# then exec the daemon in the foreground for supervisord.
set -e
mkdir -p /run/dbus /var/lib/dbus
[ -s /var/lib/dbus/machine-id ] || dbus-uuidgen > /var/lib/dbus/machine-id
rm -f /run/dbus/pid
exec dbus-daemon --system --nofork --nopidfile

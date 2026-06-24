#!/bin/bash
# x11vnc `-pipeinput` child: forward the input-event text stream x11vnc writes to our stdin into
# the FIFO that the persistent vnc_input_daemon owns. Keeping the device-owning daemon SEPARATE
# from x11vnc (rather than letting x11vnc create its own UINPUT device) is what lets x11vnc
# restart without re-creating the uinput devices — which would break the one-shot libinput
# enumeration in v5_spectate.sh setup_uinput(). `reopen` on the x11vnc side respawns this if the
# daemon/FIFO isn't up yet at boot.
exec cat >> "${VNC_FIFO:-/tmp/vnc_input.fifo}"

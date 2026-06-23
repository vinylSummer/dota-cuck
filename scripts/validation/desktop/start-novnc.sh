#!/bin/bash
# noVNC web frontend (websockify) bridging :6080 -> the x11vnc :5900 socket, so the operator
# can complete the one-time Steam login from a browser over an SSH tunnel. Gated by ENABLE_VNC.
if [ "${ENABLE_VNC:-true}" != "true" ]; then
    echo "start-novnc: ENABLE_VNC!=true, idling"
    exec sleep infinity
fi

# Debian's novnc ships at /usr/share/novnc with vnc.html (and vnc_lite.html).
exec websockify --web=/usr/share/novnc 6080 localhost:5900

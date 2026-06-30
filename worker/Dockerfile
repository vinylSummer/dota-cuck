# Production worker image: the validated headless GPU + Steam + Dota stack
# (dota-steam) plus the uv-managed worker project and a supervisord that brings
# the desktop up and runs the worker agent (WORKER_DOTA_BRINGUP=1).
#
# Build the validated base first — it installs the full NVIDIA userspace driver,
# the Steam/Dota toolchain, tesseract/imagemagick, evdev, dumb-udev and the
# desktop scripts (see scripts/validation/Dockerfile.{xtest,steam}):
#   docker build -f scripts/validation/Dockerfile.xtest -t dota-xtest .
#   docker build -f scripts/validation/Dockerfile.steam -t dota-steam .
# (override the base with --build-arg BASE_IMAGE=...).
#
# uinput device ordering: the input daemons (uinput_daemon, vnc_input_daemon)
# create the devices, a one-shot (input-bind.sh) ensures their /dev/input nodes +
# retags the udev db (the proven setup_uinput logic), and Xorg is GATED on that
# (run-xorg.sh waits for /tmp/uinput-ready) so its startup enumeration binds them
# via libinput with no restart. DotaClient drives the daemons' FIFOs. Validate
# live on the GPU host (compare to the harness dota-v5 reference).
ARG BASE_IMAGE=dota-steam
FROM ${BASE_IMAGE}

# build-essential + python3.10 headers come via uv's managed interpreter; gcc is
# needed to build evdev's C extension under that interpreter.
RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential \
    && rm -rf /var/lib/apt/lists/*

# uv-managed Python 3.10 worker env (matches the worker project's pins exactly).
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
COPY worker/ /opt/worker/
RUN cd /opt/worker && uv sync --frozen --no-dev \
    && chown -R worker:worker /opt/worker

# Production process map: the validated desktop services (udev/dbus/Xorg/Xfce +
# optional VNC for the one-time Steam login) plus the worker program.
COPY worker/deploy/supervisord.conf /etc/supervisord.worker.conf
COPY worker/deploy/run-worker.sh worker/deploy/run-xorg.sh worker/deploy/input-bind.sh \
     /usr/local/bin/
RUN chmod +x /usr/local/bin/run-worker.sh /usr/local/bin/run-xorg.sh \
             /usr/local/bin/input-bind.sh

# ENABLE_VNC=true once, over an SSH tunnel, for the one-time interactive Steam
# login (V4); thereafter the persisted token silent-auto-logins with it false.
ENV DISPLAY=:99 \
    HOME=/fard/steam/steamhome \
    WORKER_DOTA_BRINGUP=1 \
    ENABLE_VNC=false

ENTRYPOINT ["dumb-init", "--"]
CMD ["supervisord", "-c", "/etc/supervisord.worker.conf"]

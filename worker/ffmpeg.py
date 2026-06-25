"""FFmpeg pipeline: capture the headless :99 display and publish it to mediamtx over SRT.

The validated path (V6, see docs/validation-results.md) is
``x11grab :99 -> hevc_nvenc -> SRT -> mediamtx``, real-time at 60fps / ~2.3 Mbps H.265. mediamtx
re-publishes it as WebRTC/WHEP for the browser; the control plane maps the stream path to the WHEP
URL. The worker only runs the encoder and reports the SRT URL it published to via StreamStarted.

The command builders (``build_srt_url`` / ``build_ffmpeg_command``) are pure and unit-tested; the
subprocess lifecycle (start/poll/stop) is I/O, validated live.

Key facts baked in from V6:
- ``NVIDIA_DRIVER_CAPABILITIES=all`` (the ``video`` cap) is required for NVENC in the container.
- mediamtx's SRT streamid MUST carry the ``publish:`` prefix (``publish:live/match``); a bare
  ``streamid=live/match`` is publish-ambiguous and mediamtx rejects it.
"""

from __future__ import annotations

import logging
import os
import subprocess
import time
from dataclasses import dataclass

log = logging.getLogger("worker.ffmpeg")


# ============================== pure command builders (unit-tested) ==============================


@dataclass
class FFmpegConfig:
    display: str = ":99"
    screen_w: int = 1280
    screen_h: int = 720
    fps: int = 60
    bitrate: str = "4M"
    preset: str = "p4"  # NVENC p1(fast)..p7(slow); p4 is the validated balance
    srt_host: str = "127.0.0.1"
    srt_port: int = 8890
    stream_path: str = "live/match"  # the single V1 mediamtx path

    @classmethod
    def from_env(cls) -> "FFmpegConfig":
        w, _, h = os.environ.get("SCREEN", "1280x720").partition("x")
        return cls(
            display=os.environ.get("DISPLAY", ":99"),
            screen_w=int(w or 1280),
            screen_h=int(h or 720),
            srt_host=os.environ.get("MEDIAMTX_SRT_HOST", "127.0.0.1"),
            srt_port=int(os.environ.get("MEDIAMTX_SRT_PORT", "8890")),
            stream_path=os.environ.get("STREAM_PATH", "live/match"),
        )


def build_srt_url(host: str, port: int, path: str) -> str:
    """mediamtx SRT publish URL. The ``publish:`` prefix on the streamid is mandatory (V6)."""
    return f"srt://{host}:{port}?streamid=publish:{path}"


def build_ffmpeg_command(cfg: FFmpegConfig) -> list[str]:
    """The validated x11grab -> hevc_nvenc -> SRT/mpegts command (no -t: runs until stopped)."""
    return [
        "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
        "-f", "x11grab", "-r", str(cfg.fps), "-s", f"{cfg.screen_w}x{cfg.screen_h}",
        "-i", cfg.display,
        "-c:v", "hevc_nvenc", "-preset", cfg.preset, "-b:v", cfg.bitrate,
        "-f", "mpegts", build_srt_url(cfg.srt_host, cfg.srt_port, cfg.stream_path),
    ]


# ===================================== I/O: FFmpegPipeline =====================================


class FFmpegError(Exception):
    """The encoder failed to start or publish."""


class FFmpegPipeline:
    """Owns the long-lived ffmpeg encoder subprocess for one spectate session."""

    def __init__(self, config: FFmpegConfig | None = None,
                 startup_settle_seconds: float = 2.5) -> None:
        self.cfg = config or FFmpegConfig.from_env()
        self.startup_settle_seconds = startup_settle_seconds
        self._proc: subprocess.Popen | None = None
        self._log = None

    @property
    def srt_url(self) -> str:
        return build_srt_url(self.cfg.srt_host, self.cfg.srt_port, self.cfg.stream_path)

    def is_running(self) -> bool:
        return self._proc is not None and self._proc.poll() is None

    def start(self) -> str:
        """Launch the encoder and confirm it stays up past a brief settle (a failed SRT connect or
        NVENC init makes ffmpeg exit within ~1s). Returns the SRT URL it published to. Idempotent.
        Raises FFmpegError if the encoder dies immediately."""
        if self.is_running():
            return self.srt_url
        cmd = build_ffmpeg_command(self.cfg)
        env = {**os.environ, "DISPLAY": self.cfg.display}
        logpath = os.path.join(os.environ.get("HOME", "/tmp"), "worker-ffmpeg.log")
        self._log = open(logpath, "ab")  # noqa: SIM115 — handed to the child for its lifetime
        log.info("starting ffmpeg encoder -> %s", self.srt_url)
        self._proc = subprocess.Popen(
            cmd, env=env, stdout=subprocess.DEVNULL, stderr=self._log,
        )
        time.sleep(self.startup_settle_seconds)  # SRT connect + first encoded frames
        if self._proc.poll() is not None:
            rc = self._proc.returncode
            self._close_log()
            self._proc = None
            raise FFmpegError(f"ffmpeg exited immediately (rc={rc}); see {logpath}")
        return self.srt_url

    def stop(self) -> None:
        """Terminate the encoder (SIGTERM, then SIGKILL). Safe to call when not running."""
        p = self._proc
        if p is not None and p.poll() is None:
            p.terminate()
            try:
                p.wait(timeout=5)
            except subprocess.TimeoutExpired:
                p.kill()
                try:
                    p.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    log.warning("ffmpeg did not exit after SIGKILL")
        self._proc = None
        self._close_log()

    def _close_log(self) -> None:
        if self._log is not None:
            try:
                self._log.close()
            except Exception:  # noqa: BLE001
                pass
            self._log = None

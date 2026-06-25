"""Unit tests for the pure FFmpeg command builders. The subprocess lifecycle is I/O (validated
live in V6); only the argument/URL construction is tested — the bits that, if wrong, silently break
the encode or the mediamtx publish (e.g. a missing ``publish:`` streamid prefix)."""

from ffmpeg import FFmpegConfig, build_ffmpeg_command, build_srt_url


def test_srt_url_carries_publish_prefix():
    # mediamtx rejects a bare streamid=live/match as publish-ambiguous (V6 finding).
    assert build_srt_url("127.0.0.1", 8890, "live/match") == \
        "srt://127.0.0.1:8890?streamid=publish:live/match"


def test_ffmpeg_command_is_the_validated_pipeline():
    cfg = FFmpegConfig(display=":99", screen_w=1280, screen_h=720, fps=60,
                       bitrate="4M", preset="p4", srt_host="127.0.0.1", srt_port=8890,
                       stream_path="live/match")
    cmd = build_ffmpeg_command(cfg)

    # x11grab input at the configured size/rate off the right display.
    assert cmd[:1] == ["ffmpeg"]
    assert "-f" in cmd and "x11grab" in cmd
    assert cmd[cmd.index("-i") + 1] == ":99"
    assert cmd[cmd.index("-s") + 1] == "1280x720"
    assert cmd[cmd.index("-r") + 1] == "60"
    # hardware H.265 encode, no CPU fallback.
    assert cmd[cmd.index("-c:v") + 1] == "hevc_nvenc"
    assert cmd[cmd.index("-preset") + 1] == "p4"
    assert cmd[cmd.index("-b:v") + 1] == "4M"
    # mpegts over SRT to mediamtx, publish streamid.
    assert cmd[cmd.index("-f", cmd.index("x11grab") + 1) + 1] == "mpegts"
    assert cmd[-1] == "srt://127.0.0.1:8890?streamid=publish:live/match"
    # runs until stopped — no -t duration cap (that was only in the probe).
    assert "-t" not in cmd


def test_command_reflects_config_overrides():
    cfg = FFmpegConfig(screen_w=1920, screen_h=1080, fps=30, bitrate="8M",
                       srt_host="mediamtx", srt_port=9999, stream_path="live/match")
    cmd = build_ffmpeg_command(cfg)
    assert cmd[cmd.index("-s") + 1] == "1920x1080"
    assert cmd[cmd.index("-r") + 1] == "30"
    assert cmd[-1] == "srt://mediamtx:9999?streamid=publish:live/match"


def test_from_env_reads_screen_and_mediamtx(monkeypatch):
    monkeypatch.setenv("SCREEN", "1600x900")
    monkeypatch.setenv("DISPLAY", ":7")
    monkeypatch.setenv("MEDIAMTX_SRT_HOST", "mediamtx")
    monkeypatch.setenv("MEDIAMTX_SRT_PORT", "8899")
    cfg = FFmpegConfig.from_env()
    assert (cfg.screen_w, cfg.screen_h) == (1600, 900)
    assert cfg.display == ":7"
    assert cfg.srt_host == "mediamtx"
    assert cfg.srt_port == 8899


def test_srt_url_property_matches_builder():
    cfg = FFmpegConfig(srt_host="h", srt_port=1, stream_path="live/match")
    from ffmpeg import FFmpegPipeline
    assert FFmpegPipeline(cfg).srt_url == build_srt_url("h", 1, "live/match")

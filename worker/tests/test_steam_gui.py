"""Unit tests for the pure connection-log parser that gates Dota launch on this run's GUI Steam
logon. The process/dbus/FS glue on SteamGui is validated live; only the decision is tested.

connection_log.txt is append-only across runs, so the parser must ignore logons before the mark
(the line count snapshotted right before this run's launch) and report the LAST transition after it.
"""

from steam_gui import is_logged_on


def _log(*lines):
    return "\n".join(lines)


def test_logged_on_after_mark():
    text = _log("old stuff", "Logged On previous run")
    mark = len(text.splitlines())  # everything so far is a prior run
    text += "\n" + _log("connecting", "Logged On")
    assert is_logged_on(text, mark)


def test_logged_off_after_mark_is_false():
    text = _log("Logged On previous run")
    mark = len(text.splitlines())
    text += "\n" + _log("Logged On", "later Logged Off")
    assert not is_logged_on(text, mark)


def test_stale_logon_before_mark_ignored():
    # The only "Logged On" is before the mark; this run added nothing -> not logged on.
    text = _log("Logged On stale", "RTT measurement", "background chatter")
    assert not is_logged_on(text, mark=1)


def test_truncation_falls_back_to_whole_file():
    # mark exceeds the line count (log rotated/truncated) -> scan the whole file.
    text = _log("Logged On after rotation")
    assert is_logged_on(text, mark=999)


def test_last_transition_wins():
    text = _log("Logged Off", "Logged On", "Logged Off", "Logged On")
    assert is_logged_on(text, mark=0)
    text2 = _log("Logged On", "Logged Off")
    assert not is_logged_on(text2, mark=0)


def test_empty_log_is_not_logged_on():
    assert not is_logged_on("", mark=0)
    assert not is_logged_on("", mark=5)


def test_no_transition_lines_is_not_logged_on():
    assert not is_logged_on(_log("connecting", "RTT 40ms", "heartbeat"), mark=0)


def test_case_insensitive():
    assert is_logged_on(_log("LOGGED ON"), mark=0)
    assert not is_logged_on(_log("logged off"), mark=0)

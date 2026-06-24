"""Unit tests for the pure decision logic of the Dota GUI spectate automation.

Per docs/testing.md we test the decisions, not the uinput/OCR glue: the modal classifier, the
docked-panel detector, the OCR-font fuzzy matching, the tesseract-TSV parser, and the word-box
finder that locates the friend row + the WATCH menu item. The I/O step machine on ``DotaClient`` is
validated live in the harness (gui_spectate.py); importing this module must not need evdev (the
device import is lazy, inside ``DotaClient.setup``).

Fixtures use OCR strings catalogued live on 2026-06-24, including the measured Dota-font confusions
("zitraks mops" reading as "ritraks mops").
"""

from dota_client import (
    Box,
    SPECTATE_LABELS,
    classify_state,
    find_text_box,
    fuzzy_equal,
    panel_open,
    parse_tsv,
)


# --- classify_state (drives CLEAR_MODALS + the dashboard gate) ---------------


def test_classify_dashboard():
    assert classify_state("PLAY DOTA   HEROES   STORE") == "DASHBOARD"


def test_classify_each_modal():
    assert classify_state("Your client is OUT OF DATE") == "UPDATE_REQUIRED"
    assert classify_state("UPDATE REQUIRED to continue") == "UPDATE_REQUIRED"
    assert classify_state("PLAYER BEHAVIOR SUMMARY") == "BEHAVIOR_SUMMARY"
    assert classify_state("PARTY INVITATION from a friend") == "PARTY_INVITE"
    assert classify_state("WELCOME TO DOTA PLUS") == "DOTA_PLUS"


def test_modal_over_dashboard_classifies_as_modal():
    # A modal sitting on top of the dashboard still shows PLAY DOTA behind it; the modal wins
    # because MODAL_SIGNATURES is checked before the dashboard signature.
    text = "PLAY DOTA\nPLAYER BEHAVIOR SUMMARY\nYour conduct summary"
    assert classify_state(text) == "BEHAVIOR_SUMMARY"


def test_classify_unknown():
    assert classify_state("loading some unrecognized overlay") == "UNKNOWN"
    assert classify_state("") == "UNKNOWN"


def test_classify_is_case_and_whitespace_insensitive():
    assert classify_state("   play   dota   ") == "DASHBOARD"


# --- panel_open (docked friends panel detector) ------------------------------


def test_panel_open_signatures():
    assert panel_open("IN DOTA (3)")
    assert panel_open("SEARCH FRIENDS")
    assert panel_open("ADD FRIEND")
    assert panel_open("in dota\nzitraks mops")


def test_panel_open_negative():
    assert not panel_open("PLAY DOTA")
    assert not panel_open("")


# --- fuzzy_equal (OCR-font tolerant comparison) ------------------------------


def test_fuzzy_equal_exact():
    assert fuzzy_equal("Watch Game", "WATCH GAME")


def test_fuzzy_equal_font_confusions():
    # K<->R and I<->T are measured Dota-font confusions; the keys fold them out.
    assert fuzzy_equal("WATCH FKIEND LIVE", "Watch Friend Live")


def test_fuzzy_equal_substring():
    assert fuzzy_equal("mops", "zitraks mops")


def test_fuzzy_equal_rejects_unrelated():
    assert not fuzzy_equal("Watch Game", "Add Friend")
    assert not fuzzy_equal("", "anything")


# --- parse_tsv (tesseract TSV -> screen-space boxes) -------------------------

_TSV_HEADER = "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext"


def _tsv(rows):
    """Build a tesseract TSV string. Each row is (left, top, width, height, conf, text)."""
    lines = [_TSV_HEADER]
    for left, top, w, h, conf, text in rows:
        lines.append(f"5\t1\t1\t1\t1\t1\t{left}\t{top}\t{w}\t{h}\t{conf}\t{text}")
    return "\n".join(lines)


def test_parse_tsv_maps_back_to_screen_pixels():
    # region origin (100, 50); 3x upscale -> a TSV box at (300,150) 60x30 maps to
    # 100 + 300/3 = 200, 50 + 150/3 = 100, w 20 h 10, center (210, 105).
    boxes = parse_tsv(_tsv([(300, 150, 60, 30, 90.0, "mops")]),
                      region_x=100, region_y=50, scale=3.0)
    assert len(boxes) == 1
    b = boxes[0]
    assert (b.left, b.top, b.width, b.height) == (200, 100, 20, 10)
    assert (b.cx, b.cy) == (210, 105)
    assert b.text == "mops"


def test_parse_tsv_drops_negative_conf_and_blank():
    boxes = parse_tsv(_tsv([
        (0, 0, 10, 10, -1.0, "noise"),   # tesseract emits conf -1 for non-word rows
        (0, 0, 10, 10, 80.0, "   "),     # blank text
        (0, 0, 10, 10, 80.0, "real"),
    ]))
    assert [b.text for b in boxes] == ["real"]


def test_parse_tsv_non_tsv_input_returns_empty():
    assert parse_tsv("just some plain text output") == []
    assert parse_tsv("") == []


# --- find_text_box (locate friend row + WATCH menu item) ---------------------


def _box(text, cx=0, cy=0, conf=90.0):
    return Box(text, cx, cy, cx, cy, 10, 10, conf)


def test_find_friend_full_phrase():
    boxes = [_box("ritraks", 100, 40), _box("mops", 160, 40), _box("PLAY", 600, 700)]
    hit = find_text_box(boxes, "zitraks mops")
    assert hit is not None
    # merged box spans both words; center sits between them.
    assert 100 <= hit.cx <= 170


def test_find_friend_single_distinctive_word_fallback():
    # The first persona word is wrecked by OCR but "mops" reads clean; the >=4-char single-word
    # fallback still locates the row (clicking anywhere on it works).
    boxes = [_box("xQz9", 100, 40), _box("mops", 160, 40)]
    hit = find_text_box(boxes, "zitraks mops")
    assert hit is not None and hit.text == "mops"


def test_find_menu_item_among_spectate_labels():
    boxes = [_box("Watch", 400, 580), _box("Friend", 460, 580), _box("Live", 520, 580),
             _box("Add", 400, 620), _box("Friend", 460, 620)]
    found = None
    for label in SPECTATE_LABELS:
        found = find_text_box(boxes, label)
        if found:
            break
    assert found is not None
    assert "watch" in found.text.lower()


def test_find_text_box_respects_conf_threshold():
    boxes = [_box("mops", 160, 40, conf=20.0)]  # below the default min_conf 40
    assert find_text_box(boxes, "zitraks mops") is None


def test_find_text_box_miss():
    boxes = [_box("Heroes", 100, 40), _box("Store", 200, 40)]
    assert find_text_box(boxes, "zitraks mops") is None

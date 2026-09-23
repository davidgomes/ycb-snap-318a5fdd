import itertools

import pytest

from textual._xterm_parser import XTermParser
from textual.events import (
    Key,
    MouseDown,
    MouseMove,
    MouseScrollDown,
    MouseScrollLeft,
    MouseScrollRight,
    MouseScrollUp,
    MouseUp,
    Paste,
)
from textual.messages import TerminalSupportsSynchronizedOutput


def chunks(data, size):
    if size == 0:
        yield data
        return

    chunk_start = 0
    chunk_end = size
    while True:
        yield data[chunk_start:chunk_end]
        chunk_start = chunk_end
        chunk_end += size
        if chunk_end >= len(data):
            yield data[chunk_start:chunk_end]
            break


@pytest.fixture
def parser():
    return XTermParser()


@pytest.mark.parametrize("chunk_size", [2, 3, 4, 5, 6])
def test_varying_parser_chunk_sizes_no_missing_data(parser, chunk_size):
    end = "\x1b[8~"
    text = "ABCDEFGH"

    data = end + text
    events = []
    for chunk in chunks(data, chunk_size):
        events.append(parser.feed(chunk))

    events = list(itertools.chain.from_iterable(list(event) for event in events))

    assert events[0].key == "end"
    assert [event.key for event in events[1:]] == list(text)


def test_bracketed_paste(parser):
    """When bracketed paste mode is enabled in the terminal emulator and
    the user pastes in some text, it will surround the pasted input
    with the escape codes "\x1b[200~" and "\x1b[201~". The text between
    these codes corresponds to a single `Paste` event in Textual.
    """
    pasted_text = "PASTED"
    events = list(parser.feed(f"\x1b[200~{pasted_text}\x1b[201~"))

    assert len(events) == 1
    assert isinstance(events[0], Paste)
    assert events[0].text == pasted_text


def test_bracketed_paste_content_contains_escape_codes(parser):
    """When performing a bracketed paste, if the pasted content contains
    supported ANSI escape sequences, it should not interfere with the paste,
    and no escape sequences within the bracketed paste should be converted
    into Textual events.
    """
    pasted_text = "PAS\x0fTED"
    events = list(parser.feed(f"\x1b[200~{pasted_text}\x1b[201~"))
    assert len(events) == 1
    assert events[0].text == pasted_text


def test_bracketed_paste_amongst_other_codes(parser):
    pasted_text = "PASTED"
    events = list(parser.feed(f"\x1b[8~\x1b[200~{pasted_text}\x1b[201~\x1b[8~"))
    assert len(events) == 3  # Key.End -> Paste -> Key.End
    assert events[0].key == "end"
    assert events[1].text == pasted_text
    assert events[2].key == "end"


def test_cant_match_escape_sequence_too_long(parser):
    """The sequence did not match, and we hit the maximum sequence search
    length threshold, so each character should be issued as a key-press instead.
    """
    sequence = "\x1b[123456789123456789123123456789123456789123"
    events = list(parser.feed(sequence))

    # Every character in the sequence is converted to a key press
    assert len(events) == len(sequence)
    assert all(isinstance(event, Key) for event in events)

    # When we backtrack '\x1b' is translated to '^'
    assert events[0].key == "circumflex_accent"

    # The rest of the characters correspond to the expected key presses
    events = events[1:]
    for index, character in enumerate(sequence[1:]):
        assert events[index].character == character


@pytest.mark.parametrize(
    "chunk_size",
    [
        2,
        3,
        4,
        5,
        6,
    ],
)
def test_unknown_sequence_followed_by_known_sequence(parser, chunk_size):
    """When we feed the parser an unknown sequence followed by a known
    sequence. The characters in the unknown sequence are delivered as keys,
    and the known escape sequence that follows is delivered as expected.
    """
    unknown_sequence = "\x1b[?"
    known_sequence = "\x1b[8~"  # key = 'end'

    sequence = unknown_sequence + known_sequence

    events = []

    for chunk in chunks(sequence, chunk_size):
        events.extend(list(parser.feed(chunk)))

    # events = list(itertools.chain.from_iterable(list(event) for event in events))
    print(repr([event.key for event in events]))

    assert [event.key for event in events] == [
        "circumflex_accent",
        "left_square_bracket",
        "question_mark",
        "end",
    ]


def test_simple_key_presses_all_delivered_correct_order(parser):
    sequence = "123abc"
    events = parser.feed(sequence)
    assert "".join(event.key for event in events) == sequence


def test_simple_keypress_non_character_key(parser):
    sequence = "\x09"
    events = list(parser.feed(sequence))
    assert len(events) == 1
    assert events[0].key == "tab"


def test_key_presses_and_escape_sequence_mixed(parser):
    sequence = "abc\x1b[13~123"
    events = list(parser.feed(sequence))

    assert len(events) == 7
    assert "".join(event.key for event in events) == "abcf3123"


def test_single_escape(parser):
    """A single \x1b should be interpreted as a single press of the Escape key"""
    events = list(parser.feed("\x1b"))
    events.extend(parser.feed(""))
    assert [event.key for event in events] == ["escape"]


def test_double_escape(parser):
    """Test double escape."""
    events = list(parser.feed("\x1b\x1b"))
    events.extend(parser.feed(""))
    print(events)
    assert [event.key for event in events] == ["escape", "escape"]


@pytest.mark.parametrize(
    "sequence,key",
    [
        ("a", "a"),
        ("B", "B"),
        ("\x1ba", "alt+a"),
        ("\x1b[97;3u", "alt+a"),
        ("\x1b[65;4u", "alt+shift+a"),
        ("\x1bA", "alt+shift+a"),
        ("\x1b[120;7u", "alt+ctrl+x"),
    ],
)
def test_keys(parser, sequence: str, key: str) -> None:
    """Test rarer keys."""
    events = []
    for event in parser.feed(sequence):
        events.append(event)
    for event in parser.feed(""):
        events.append(event)
    event = events[0]
    assert event.key == key


def parse_key(parser: XTermParser, sequence: str) -> Key:
    """Feed a sequence to the parser, and return the single key event it produces."""
    events = [*parser.feed(sequence), *parser.feed("")]
    assert len(events) == 1
    assert isinstance(events[0], Key)
    return events[0]


@pytest.mark.parametrize(
    "sequence, key, phase",
    [
        ("\x1b[97u", "a", "press"),
        ("\x1b[97;1:1u", "a", "press"),
        ("\x1b[97;1:2u", "a", "repeat"),
        ("\x1b[97;1:3u", "a", "release"),
        ("\x1b[99;5:3u", "ctrl+c", "release"),
        ("\x1b[13;1:3u", "enter", "release"),
        ("\x1b[1;1:3A", "up", "release"),
        ("\x1b[1;5:2C", "ctrl+right", "repeat"),
        ("\x1b[3;1:3~", "delete", "release"),
    ],
)
def test_kitty_key_phases(parser, sequence: str, key: str, phase: str) -> None:
    """Kitty keyboard protocol event types are reported as the key phase."""
    event = parse_key(parser, sequence)
    assert event.key == key
    assert event.phase == phase
    assert event.is_press is (phase == "press")
    assert event.is_repeat is (phase == "repeat")
    assert event.is_release is (phase == "release")


def test_kitty_key_release_is_not_printable(parser) -> None:
    event = parse_key(parser, "\x1b[97;1:3u")
    assert event.character == "a"
    assert not event.is_printable


@pytest.mark.parametrize(
    "sequence",
    [
        "\x1b[97;2u",
        "\x1b[97:65;2u",
        "\x1b[97;2;65u",
        "\x1b[97:65:97;2;65u",
    ],
)
def test_kitty_shift_printable_key(parser, sequence: str) -> None:
    """Shift with a printable key produces the shifted character."""
    event = parse_key(parser, sequence)
    assert event.key in ("A", "shift+a")
    assert event.character == "A"
    assert event.is_printable
    assert event.modifiers == ("shift",)
    assert event.shift
    assert event.base_key == "a"


def test_kitty_shift_printable_key_uses_shifted_key(parser) -> None:
    event = parse_key(parser, "\x1b[49:33;2u")
    assert event.character == "!"
    assert event.modifiers == ("shift",)
    assert event.base_key == "1"
    assert event.shifted_key == "exclamation_mark"


@pytest.mark.parametrize(
    "sequence, key, modifiers",
    [
        ("\x1b[97:65;4u", "alt+shift+a", ("alt", "shift")),
        ("\x1b[97;4;65u", "alt+shift+a", ("alt", "shift")),
        ("\x1b[97;5u", "ctrl+a", ("ctrl",)),
        ("\x1b[97;9u", "super+a", ("super",)),
        (
            "\x1b[97;64u",
            "alt+ctrl+hyper+meta+shift+super+a",
            ("alt", "ctrl", "hyper", "meta", "shift", "super"),
        ),
    ],
)
def test_kitty_modified_printable_key(
    parser, sequence: str, key: str, modifiers
) -> None:
    """Printable keys with modifiers other than shift are shortcuts, not text."""
    event = parse_key(parser, sequence)
    assert event.key == key
    assert event.character is None
    assert not event.is_printable
    assert event.modifiers == modifiers
    assert event.base_key == "a"


def test_kitty_modifier_properties(parser) -> None:
    event = parse_key(parser, "\x1b[97;64u")
    assert event.shift
    assert event.alt
    assert event.ctrl
    assert event.super
    assert event.hyper
    assert event.meta


def test_kitty_lock_modifiers_ignored(parser) -> None:
    """The caps_lock and num_lock bits are not reported as modifiers."""
    event = parse_key(parser, "\x1b[97;197u")  # ctrl + caps_lock + num_lock
    assert event.key == "ctrl+a"
    assert event.modifiers == ("ctrl",)


def test_kitty_text_event(parser) -> None:
    """A key code of 0 reports text with no associated key."""
    event = parse_key(parser, "\x1b[0;;229u")
    assert event.key == "å"
    assert event.character == "å"
    assert event.is_printable


def test_kitty_associated_text(parser) -> None:
    event = parse_key(parser, "\x1b[101;;233u")
    assert event.character == "é"
    assert event.base_key == "e"


def test_kitty_shifted_key_alias(parser) -> None:
    """A reported shifted key lets shortcuts match the shifted form."""
    event = parse_key(parser, "\x1b[61:43;6u")
    assert event.key == "ctrl+shift+equals_sign"
    assert event.character is None
    assert event.modifiers == ("ctrl", "shift")
    assert event.base_key == "equals_sign"
    assert event.shifted_key == "plus"
    assert "ctrl+plus" in event.aliases
    assert "ctrl_plus" in event.name_aliases


def test_kitty_base_layout_key(parser) -> None:
    """The base layout key is reported for non-latin layouts."""
    event = parse_key(parser, "\x1b[1089::99;5u")
    assert event.key == "ctrl+с"
    assert event.shifted_key is None
    assert event.base_layout_key == "c"


def test_kitty_invalid_codepoint(parser) -> None:
    """An invalid codepoint doesn't raise, and is reissued as keys."""
    sequence = "\x1b[9999999u"
    events = [*parser.feed(sequence), *parser.feed("")]
    assert "".join(event.character for event in events) == sequence[1:]


@pytest.mark.parametrize(
    "sequence, key, character, modifiers, base_key",
    [
        ("\x1b\r", "alt+enter", "\r", ("alt",), "enter"),
        ("\x1b ", "alt+space", " ", ("alt",), "space"),
        ("\x1b\x08", "alt+backspace", "\x08", ("alt",), "backspace"),
        ("\x1b\x01", "alt+ctrl+a", "\x01", ("alt", "ctrl"), "a"),
        ("\x1b\x1a", "alt+ctrl+z", "\x1a", ("alt", "ctrl"), "z"),
        ("\x1ba", "alt+a", "a", ("alt",), "a"),
        ("\x1bA", "alt+shift+a", "A", ("alt", "shift"), "a"),
    ],
)
def test_legacy_alt_keys(
    parser, sequence: str, key: str, character: str, modifiers, base_key: str
) -> None:
    """ESC-prefixed legacy keys report alt, with metadata that agrees with the key."""
    event = parse_key(parser, sequence)
    assert event.key == key
    assert event.character == character
    assert event.phase == "press"
    assert event.modifiers == modifiers
    assert event.alt
    assert event.base_key == base_key


@pytest.mark.parametrize(
    "sequence, event_type, shift, meta",
    [
        # Mouse down, with and without modifiers
        ("\x1b[<0;50;25M", MouseDown, False, False),
        ("\x1b[<4;50;25M", MouseDown, True, False),
        ("\x1b[<8;50;25M", MouseDown, False, True),
        ("\x1b[<12;50;25M", MouseDown, True, True),
        # Mouse up, with and without modifiers
        ("\x1b[<0;50;25m", MouseUp, False, False),
        ("\x1b[<4;50;25m", MouseUp, True, False),
        ("\x1b[<8;50;25m", MouseUp, False, True),
        ("\x1b[<12;50;25m", MouseUp, True, True),
    ],
)
def test_mouse_click(parser, sequence, event_type, shift, meta):
    """ANSI codes for mouse should be converted to Textual events"""
    events = list(parser.feed(sequence))

    assert len(events) == 1

    event = events[0]

    assert isinstance(event, event_type)
    assert event.x == 49
    assert event.y == 24
    assert event.screen_x == 49
    assert event.screen_y == 24
    assert event.meta is meta
    assert event.shift is shift


@pytest.mark.parametrize(
    "sequence, shift, meta, button",
    [
        ("\x1b[<32;15;38M", False, False, 1),  # Click and drag
        ("\x1b[<35;15;38M", False, False, 0),  # Basic cursor movement
        ("\x1b[<39;15;38M", True, False, 0),  # Shift held down
        ("\x1b[<43;15;38M", False, True, 0),  # Meta held down
        ("\x1b[<3;15;38M", False, False, 0),
    ],
)
def test_mouse_move(parser, sequence, shift, meta, button):
    events = list(parser.feed(sequence))

    assert len(events) == 1

    event = events[0]

    assert isinstance(event, MouseMove)
    assert event.x == 14
    assert event.y == 37
    assert event.shift is shift
    assert event.meta is meta
    assert event.button == button


@pytest.mark.parametrize(
    "sequence, shift, meta",
    [
        ("\x1b[<64;18;25M", False, False),
        ("\x1b[<68;18;25M", True, False),
        ("\x1b[<72;18;25M", False, True),
    ],
)
def test_mouse_scroll_up(parser, sequence, shift, meta):
    """Scrolling the mouse with and without modifiers held down.
    We don't currently capture modifier keys in scroll events.
    """
    events = list(parser.feed(sequence))

    assert len(events) == 1

    event = events[0]

    assert isinstance(event, MouseScrollUp)
    assert event.x == 17
    assert event.y == 24
    assert event.shift is shift
    assert event.meta is meta


@pytest.mark.parametrize(
    "sequence, shift, meta",
    [
        ("\x1b[<65;18;25M", False, False),
        ("\x1b[<69;18;25M", True, False),
        ("\x1b[<73;18;25M", False, True),
    ],
)
def test_mouse_scroll_down(parser, sequence, shift, meta):
    events = list(parser.feed(sequence))

    assert len(events) == 1

    event = events[0]

    assert isinstance(event, MouseScrollDown)
    assert event.x == 17
    assert event.y == 24
    assert event.shift is shift
    assert event.meta is meta


@pytest.mark.parametrize(
    "sequence, shift, meta",
    [
        ("\x1b[<66;18;25M", False, False),
        ("\x1b[<70;18;25M", True, False),
        ("\x1b[<74;18;25M", False, True),
    ],
)
def test_mouse_scroll_left(parser, sequence, shift, meta):
    """Scrolling the mouse with and without modifiers held down.
    We don't currently capture modifier keys in scroll events.
    """
    events = list(parser.feed(sequence))

    assert len(events) == 1

    event = events[0]

    assert isinstance(event, MouseScrollLeft)
    assert event.x == 17
    assert event.y == 24
    assert event.shift is shift
    assert event.meta is meta


@pytest.mark.parametrize(
    "sequence, shift, meta",
    [
        ("\x1b[<67;18;25M", False, False),
        ("\x1b[<71;18;25M", True, False),
        ("\x1b[<75;18;25M", False, True),
    ],
)
def test_mouse_scroll_right(parser, sequence, shift, meta):
    """Scrolling the mouse with and without modifiers held down.
    We don't currently capture modifier keys in scroll events.
    """
    events = list(parser.feed(sequence))

    assert len(events) == 1

    event = events[0]

    assert isinstance(event, MouseScrollRight)
    assert event.x == 17
    assert event.y == 24
    assert event.shift is shift
    assert event.meta is meta


def test_mouse_event_detected_but_info_not_parsed(parser):
    # I don't know if this can actually happen in reality, but
    # there's a branch in the code that allows for the possibility.
    events = list(parser.feed("\x1b[<65;18;20;25M"))
    assert len(events) == 0


@pytest.mark.xfail()
def test_escape_sequence_resulting_in_multiple_keypresses(parser):
    """Some sequences are interpreted as more than 1 keypress"""
    events = list(parser.feed("\x1b[2;4~"))
    assert len(events) == 2
    assert events[0].key == "escape"
    assert events[1].key == "shift+insert"


@pytest.mark.parametrize("parameter", range(1, 5))
def test_terminal_mode_reporting_synchronized_output_supported(parser, parameter):
    sequence = f"\x1b[?2026;{parameter}$y"
    events = list(parser.feed(sequence))
    assert len(events) == 1
    assert isinstance(events[0], TerminalSupportsSynchronizedOutput)


def test_terminal_mode_reporting_synchronized_output_not_supported(parser):
    sequence = "\x1b[?2026;0$y"
    events = list(parser.feed(sequence))
    assert events == []

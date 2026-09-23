import importlib.util
from pathlib import Path

import pytest

from textual._xterm_parser import XTermParser
from textual.events import Key
from textual.widgets import RichLog


def parse_key(sequence: str) -> Key:
    parser = XTermParser()
    events = [*parser.feed(sequence), *parser.feed("")]
    assert len(events) == 1
    event = events[0]
    assert isinstance(event, Key)
    return event


def test_key_defaults() -> None:
    event = Key("a", "a")
    assert event.phase == "press"
    assert event.is_press and not event.is_repeat and not event.is_release
    assert event.modifiers == ()
    assert event.base_key is None
    assert event.shifted_key is None
    assert event.base_layout_key is None


def test_key_modifiers_sorted_and_properties() -> None:
    event = Key("ctrl+shift+a", None, modifiers=["shift", "ctrl"], base_key="a")
    assert event.modifiers == ("ctrl", "shift")
    assert event.ctrl and event.shift
    assert not (event.alt or event.super or event.hyper or event.meta)


def test_key_invalid_phase() -> None:
    with pytest.raises(ValueError):
        Key("a", "a", phase="pressed")


@pytest.mark.parametrize(
    "sequence, phase",
    [
        ("\x1b[97u", "press"),
        ("\x1b[97;1:1u", "press"),
        ("\x1b[97;1:2u", "repeat"),
        ("\x1b[97;1:3u", "release"),
        ("\x1b[1;1:3A", "release"),
        ("\x1b[3;5:2~", "repeat"),
    ],
)
def test_kitty_phase(sequence: str, phase: str) -> None:
    event = parse_key(sequence)
    assert event.phase == phase


def test_kitty_text_key_keeps_metadata_across_phases() -> None:
    for sequence in ("\x1b[97u", "\x1b[97;1:2u", "\x1b[97;1:3u"):
        event = parse_key(sequence)
        assert event.key == "a"
        assert event.character == "a"
        assert event.modifiers == ()
        assert event.base_key == "a"


@pytest.mark.parametrize(
    "sequence",
    ["\x1b[97;2u", "\x1b[97:65;2u", "\x1b[97;2;65u", "\x1b[97:65;2;65u"],
)
def test_kitty_shift_only_printable(sequence: str) -> None:
    event = parse_key(sequence)
    assert event.key in ("A", "shift+a")
    assert event.character == "A"
    assert event.modifiers == ("shift",)
    assert event.base_key == "a"
    assert event.shift


@pytest.mark.parametrize(
    "sequence, key",
    [
        ("\x1b[97;4u", "alt+shift+a"),
        ("\x1b[97:65;4u", "alt+shift+a"),
        ("\x1b[97;3u", "alt+a"),
        ("\x1b[120;7u", "alt+ctrl+x"),
    ],
)
def test_kitty_modified_printable(sequence: str, key: str) -> None:
    event = parse_key(sequence)
    assert event.key == key
    assert event.character is None
    assert event.modifiers == tuple(key.split("+")[:-1])


def test_kitty_associated_text_only() -> None:
    event = parse_key("\x1b[0;;233u")
    assert event.key == "é"
    assert event.character == "é"


def test_kitty_alternate_keys() -> None:
    event = parse_key("\x1b[61:43;6u")
    assert event.key == "ctrl+shift+equals_sign"
    assert event.shifted_key == "plus"
    assert event.base_key == "equals_sign"
    assert "ctrl+plus" in event.aliases

    event = parse_key("\x1b[1089::99;5u")
    assert event.base_layout_key == "c"
    assert "ctrl+c" in event.aliases


@pytest.mark.parametrize(
    "sequence, key, character, modifiers, base_key",
    [
        ("\x1b\r", "enter", "\r", ("alt",), "enter"),
        ("\x1b ", "space", " ", ("alt",), "space"),
        ("\x1b\x08", "backspace", "\x08", ("alt",), "backspace"),
        ("\x1b\x01", "ctrl+a", "\x01", ("alt", "ctrl"), "a"),
        ("\x1ba", "alt+a", "a", ("alt",), "a"),
        ("\x1bA", "alt+shift+a", "A", ("alt", "shift"), "a"),
    ],
)
def test_legacy_alt_prefixed(
    sequence: str,
    key: str,
    character: str,
    modifiers: tuple[str, ...],
    base_key: str,
) -> None:
    event = parse_key(sequence)
    assert event.key == key
    assert event.character == character
    assert event.modifiers == modifiers
    assert event.base_key == base_key
    assert event.phase == "press"


async def test_kitty_keyboard_protocol_example() -> None:
    path = Path(__file__).parent.parent / "examples" / "kitty_keyboard_protocol.py"
    spec = importlib.util.spec_from_file_location("kitty_keyboard_protocol", path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    app = module.KittyKeyboardProtocolApp()
    async with app.run_test() as pilot:
        await pilot.press("a")
        await pilot.pause()
        log = app.query_one("#events", RichLog)
        text = "\n".join(line.text for line in log.lines)
        assert "phase=press" in text
        assert "character='a'" in text

import importlib.util
from pathlib import Path

import pytest

from textual.app import App
from textual.binding import Binding
from textual.events import Key
from textual.keys import _character_to_key, format_key, key_to_character
from textual.widgets import RichLog

EXAMPLES_DIR = Path(__file__).parent.parent / "examples"


@pytest.mark.parametrize(
    "character,key",
    [
        ("1", "1"),
        ("2", "2"),
        ("a", "a"),
        ("z", "z"),
        ("_", "underscore"),
        (" ", "space"),
        ("~", "tilde"),
        ("?", "question_mark"),
        ("£", "pound_sign"),
        (",", "comma"),
    ],
)
def test_character_to_key(character: str, key: str) -> None:
    assert _character_to_key(character) == key


async def test_character_bindings():
    """Test you can bind to a character as well as a longer key name."""
    counter = 0

    class BindApp(App):
        BINDINGS = [(".,~,space", "increment", "foo")]

        def action_increment(self) -> None:
            nonlocal counter
            counter += 1

    app = BindApp()
    async with app.run_test() as pilot:
        await pilot.press(".")
        await pilot.pause()
        assert counter == 1
        await pilot.press("~")
        await pilot.pause()
        assert counter == 2
        await pilot.press(" ")
        await pilot.pause()
        assert counter == 3
        await pilot.press("x")
        await pilot.pause()
        assert counter == 3


def test_format_key():
    assert format_key("minus") == "-"


def test_get_key_display():
    app = App()

    assert app.get_key_display(Binding("p", "", "")) == "p"
    assert app.get_key_display(Binding("ctrl+p", "", "")) == "^p"
    assert app.get_key_display(Binding("right_square_bracket", "", "")) == "]"
    assert app.get_key_display(Binding("ctrl+right_square_bracket", "", "")) == "^]"
    assert (
        app.get_key_display(Binding("shift+ctrl+right_square_bracket", "", ""))
        == "shift+^]"
    )
    assert app.get_key_display(Binding("delete", "", "")) == "del"


def test_key_to_character():
    assert key_to_character("f") == "f"
    assert key_to_character("F") == "F"
    assert key_to_character("space") == " "
    assert key_to_character("ctrl+space") is None
    assert key_to_character("question_mark") == "?"
    assert key_to_character("foo") is None


def test_key_event_defaults():
    event = Key("a", "a")
    assert event.phase == "press"
    assert event.is_press
    assert not event.is_repeat
    assert not event.is_release
    assert event.modifiers == ()
    assert event.base_key == "a"
    assert event.shifted_key is None
    assert event.base_layout_key is None
    assert not any(
        (event.shift, event.alt, event.ctrl, event.super, event.hyper, event.meta)
    )


@pytest.mark.parametrize(
    "key,modifiers,base_key",
    [
        ("ctrl+a", ("ctrl",), "a"),
        ("ctrl+shift+left", ("ctrl", "shift"), "left"),
        ("alt+ctrl+a", ("alt", "ctrl"), "a"),
        ("shift+tab", ("shift",), "tab"),
        ("plus", (), "plus"),
        ("+", (), "+"),
        ("<ignore>", (), "<ignore>"),
    ],
)
def test_key_event_derives_modifiers(key, modifiers, base_key):
    """Modifiers and base key are derived from the key, if not supplied."""
    event = Key(key, None)
    assert event.modifiers == modifiers
    assert event.base_key == base_key


def test_key_event_modifiers_sorted():
    event = Key("A", "A", modifiers=["shift", "ctrl", "alt"], base_key="a")
    assert event.modifiers == ("alt", "ctrl", "shift")
    assert event.base_key == "a"


def test_key_event_invalid_phase():
    with pytest.raises(ValueError):
        Key("a", "a", phase="down")


def test_key_event_shifted_key_alias():
    event = Key(
        "ctrl+shift+equals_sign",
        None,
        modifiers=("ctrl", "shift"),
        base_key="equals_sign",
        shifted_key="plus",
    )
    assert event.aliases == ["ctrl+shift+equals_sign", "ctrl+plus"]


async def test_kitty_keyboard_protocol_example():
    """The example app logs the phase and character of key events."""
    spec = importlib.util.spec_from_file_location(
        "kitty_keyboard_protocol", EXAMPLES_DIR / "kitty_keyboard_protocol.py"
    )
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    app = module.KittyKeyboardProtocolApp()
    async with app.run_test() as pilot:
        await pilot.press("a")
        app.post_message(Key("a", "a", phase="release"))
        await pilot.pause()
        lines = [line.text for line in app.query_one("#events", RichLog).lines]
    assert "phase=press" in lines[0]
    assert "character='a'" in lines[0]
    assert "phase=release" in lines[-1]

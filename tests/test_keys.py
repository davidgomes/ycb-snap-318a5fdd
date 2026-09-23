import pytest

from textual.app import App
from textual.binding import Binding
from textual.keys import _character_to_key, format_key, key_to_character


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


def test_key_event_metadata_defaults() -> None:
    """Stored Kitty fields default to a press with modifiers taken from the key."""
    from textual.events import Key

    event = Key("alt+ctrl+a", None)
    assert event.phase == "press"
    assert event.is_press is True
    assert event.is_repeat is False
    assert event.is_release is False
    assert event.modifiers == ("alt", "ctrl")
    assert event.base_key == "a"
    assert event.shifted_key is None
    assert event.base_layout_key is None
    assert event.alt is True
    assert event.ctrl is True
    assert event.shift is False
    assert event.super is False
    assert event.hyper is False
    assert event.meta is False


async def test_shifted_key_alias_matches_binding() -> None:
    """A ctrl+plus binding matches the Kitty shifted-key alias."""
    from textual._xterm_parser import XTermParser

    pressed = False

    class PlusApp(App):
        BINDINGS = [("ctrl+plus", "hit", "Hit")]

        def action_hit(self) -> None:
            nonlocal pressed
            pressed = True

    event = list(XTermParser().feed("\x1b[61:43;6u"))[0]
    async with PlusApp().run_test() as pilot:
        assert pilot.app._driver is not None
        event.set_sender(pilot.app)
        pilot.app._driver.send_message(event)
        await pilot.pause()
    assert pressed is True

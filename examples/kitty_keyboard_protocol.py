"""
An app to log key events reported with the Kitty keyboard protocol.

The app asks the terminal to report repeat and release events, alternate keys, and
associated text. Run it in a terminal that supports the Kitty keyboard protocol
(such as Kitty, Ghostty, WezTerm, or foot) to see every phase of a key event.

Press ctrl+q to quit.
"""

from textual import events
from textual.app import App, ComposeResult
from textual.widgets import RichLog

# https://sw.kovidgoyal.net/kitty/keyboard-protocol/#progressive-enhancement
KITTY_KEYBOARD_FLAGS = (
    0b1  # Disambiguate escape codes
    | 0b10  # Report event types
    | 0b100  # Report alternate keys
    | 0b1000  # Report all keys as escape codes
    | 0b10000  # Report associated text
)


class KittyKeyboardProtocolApp(App):
    """Log key events, with their phase and modifiers."""

    def compose(self) -> ComposeResult:
        yield RichLog(id="events", highlight=True, wrap=True)

    def on_mount(self) -> None:
        if self._driver is not None:
            # Textual only requests "disambiguate escape codes". Setting (rather than
            # pushing) flags means they are restored when Textual pops them on exit.
            self._driver.write(f"\x1b[={KITTY_KEYBOARD_FLAGS};1u")

    def on_key(self, event: events.Key) -> None:
        line = (
            f"key={event.key!r} phase={event.phase} character={event.character!r} "
            f"modifiers={event.modifiers} base_key={event.base_key!r}"
        )
        # Alternate keys are only known if the terminal reports them.
        if event.shifted_key is not None:
            line += f" shifted_key={event.shifted_key!r}"
        if event.base_layout_key is not None:
            line += f" base_layout_key={event.base_layout_key!r}"
        self.query_one("#events", RichLog).write(line)


if __name__ == "__main__":
    app = KittyKeyboardProtocolApp()
    app.run()

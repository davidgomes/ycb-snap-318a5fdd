"""
Logs the key events Textual receives, including the extra detail reported
by terminals supporting the Kitty keyboard protocol
(https://sw.kovidgoyal.net/kitty/keyboard-protocol/).

Run with:

    python kitty_keyboard_protocol.py

Press ctrl+q to quit.
"""

from textual import events
from textual.app import App, ComposeResult
from textual.widgets import Footer, Header, RichLog

# disambiguate | report event types | report alternate keys
# | report all keys as escape codes | report associated text
KITTY_FLAGS = 1 | 2 | 4 | 8 | 16


class KittyKeyboardProtocolApp(App[None]):
    """Show the phase, modifiers, and alternate keys of every key event."""

    # Release events would otherwise toggle the palette straight back.
    ENABLE_COMMAND_PALETTE = False

    def compose(self) -> ComposeResult:
        yield Header()
        yield RichLog(id="events", wrap=True)
        yield Footer()

    def on_mount(self) -> None:
        self.title = "Kitty keyboard protocol"
        # Replaces the flags Textual pushed at startup; Textual pops them on exit.
        if self._driver is not None and not self.is_headless:
            self._driver.write(f"\x1b[={KITTY_FLAGS};1u")
            self._driver.flush()

    def on_key(self, event: events.Key) -> None:
        self.query_one("#events", RichLog).write(
            f"key={event.key!r} phase={event.phase} character={event.character!r} "
            f"modifiers={event.modifiers!r} base_key={event.base_key!r} "
            f"shifted_key={event.shifted_key!r} "
            f"base_layout_key={event.base_layout_key!r} aliases={event.aliases!r}"
        )


if __name__ == "__main__":
    app = KittyKeyboardProtocolApp()
    app.run()

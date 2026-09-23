"""
Displays key events reported with the Kitty keyboard protocol.

Run this in a terminal that supports the protocol (such as Kitty, WezTerm, Ghostty, or foot)
to see the phase (press / repeat / release), modifiers, and alternate key metadata of each key.

https://sw.kovidgoyal.net/kitty/keyboard-protocol/
"""

from textual import events
from textual.app import App, ComposeResult
from textual.widgets import Footer, Header, RichLog

# Disambiguate escape codes (1), report event types (2), report alternate keys (4),
# report all keys as escape codes (8), and report associated text (16).
KITTY_FLAGS = 1 | 2 | 4 | 8 | 16


class KittyKeyboardProtocolApp(App):
    """Log every key event, including repeats and releases."""

    TITLE = "Kitty keyboard protocol"
    SUB_TITLE = "Press keys to see their events (ctrl+q to quit)"

    def compose(self) -> ComposeResult:
        yield Header()
        yield RichLog(id="events")
        yield Footer()

    def on_mount(self) -> None:
        # Replaces the flags on the stack entry pushed by the driver, which is
        # popped (restoring the previous flags) when the app exits.
        if self._driver is not None:
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

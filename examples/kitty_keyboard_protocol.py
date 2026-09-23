"""Log Kitty keyboard protocol key events."""

from textual.app import App, ComposeResult
from textual.events import Key
from textual.widgets import RichLog


class KittyKeyboardProtocolApp(App):
    """Display press, repeat, and release events from the Kitty keyboard protocol."""

    def compose(self) -> ComposeResult:
        yield RichLog(id="events")

    def on_key(self, event: Key) -> None:
        events_log = self.query_one("#events", RichLog)
        events_log.write(f"phase={event.phase} character={repr(event.character)}")


if __name__ == "__main__":
    KittyKeyboardProtocolApp().run()

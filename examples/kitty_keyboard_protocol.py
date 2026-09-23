"""Show Kitty keyboard protocol key events as they arrive."""

from textual.app import App, ComposeResult
from textual.events import Key
from textual.widgets import RichLog


class KittyKeyboardProtocolApp(App):
    """Log press, repeat, and release events from the Kitty keyboard protocol."""

    def compose(self) -> ComposeResult:
        yield RichLog(id="events")

    def on_key(self, event: Key) -> None:
        self.query_one(RichLog).write(
            f"phase={event.phase} character={event.character!r} key={event.key}"
        )


if __name__ == "__main__":
    KittyKeyboardProtocolApp().run()

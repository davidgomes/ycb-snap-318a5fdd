"""Show Kitty keyboard protocol events as they arrive."""

from textual.app import App, ComposeResult
from textual.events import Key
from textual.widgets import RichLog


class KittyKeyboardProtocolApp(App[None]):
    """Log press, repeat, and release events from the Kitty keyboard protocol."""

    CSS = """
    RichLog {
        height: 1fr;
    }
    """

    def compose(self) -> ComposeResult:
        yield RichLog(id="events", highlight=True, markup=False)

    def on_key(self, event: Key) -> None:
        self.query_one("#events", RichLog).write(
            f"{event.key} phase={event.phase} character={event.character!r}"
        )


if __name__ == "__main__":
    KittyKeyboardProtocolApp().run()

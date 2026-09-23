"""Displays key events, including Kitty keyboard protocol metadata."""

from textual import events
from textual.app import App, ComposeResult
from textual.widgets import RichLog


class KittyKeyboardProtocolApp(App):
    """Log every key event with its phase and modifier metadata."""

    def compose(self) -> ComposeResult:
        yield RichLog(id="events", markup=False)

    def on_key(self, event: events.Key) -> None:
        self.query_one("#events", RichLog).write(
            f"key={event.key} phase={event.phase} character={event.character!r} "
            f"modifiers={event.modifiers} base_key={event.base_key} "
            f"shifted_key={event.shifted_key} base_layout_key={event.base_layout_key}"
        )


if __name__ == "__main__":
    KittyKeyboardProtocolApp().run()

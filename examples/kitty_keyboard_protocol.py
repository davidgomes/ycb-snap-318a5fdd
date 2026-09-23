"""Show Kitty keyboard protocol phase and character on each key event."""

from __future__ import annotations

from textual.app import App, ComposeResult
from textual.events import Key
from textual.widgets import RichLog


class KittyKeyboardProtocolApp(App[None]):
    """Log press, repeat, and release events from the Kitty keyboard protocol."""

    def compose(self) -> ComposeResult:
        yield RichLog(id="events", highlight=True, markup=True)

    def on_key(self, event: Key) -> None:
        phase = event.phase
        character = event.character
        line = "phase=<phase> character=<repr(character)>"
        line = line.replace("<phase>", phase).replace(
            "<repr(character)>", repr(character)
        )
        self.query_one("#events", RichLog).write(f"{event.key} {line}")


if __name__ == "__main__":
    KittyKeyboardProtocolApp().run()

"""Demonstrate Log and RichLog follow state."""

from __future__ import annotations

from rich.text import Text

from textual.app import App, ComposeResult
from textual.containers import Horizontal, Vertical
from textual.widgets import Button, Footer, Header, Log, RichLog


class RichLogFollowStateApp(App):
    """Show follow, append, and expanded-write controls for Log and RichLog."""

    CSS = """
    #controls {
        height: auto;
        dock: top;
    }
    #log, #rich-log, #events {
        height: 1fr;
        border: solid $primary;
    }
    """

    def compose(self) -> ComposeResult:
        yield Header()
        with Horizontal(id="controls"):
            yield Button("Follow log", id="follow-log")
            yield Button("Follow rich", id="follow-rich")
            yield Button("Write expanded", id="write-expanded")
            yield Button("Append log", id="append-log")
            yield Button("Append rich", id="append-rich")
            yield Button("Clear events", id="clear-events")
        with Vertical():
            yield Log(id="log")
            yield RichLog(id="rich-log")
            yield RichLog(id="events")
        yield Footer()

    def on_button_pressed(self, event: Button.Pressed) -> None:
        button_id = event.button.id
        if button_id == "follow-log":
            self.query_one("#log", Log).follow_end()
        elif button_id == "follow-rich":
            self.query_one("#rich-log", RichLog).follow_end()
        elif button_id == "write-expanded":
            self.query_one("#rich-log", RichLog).write(
                Text("expanded entry", style="on red", justify="right"),
                expand=True,
            )
        elif button_id == "append-log":
            self.query_one("#log", Log).write_line("log line")
        elif button_id == "append-rich":
            self.query_one("#rich-log", RichLog).write("rich line")
        elif button_id == "clear-events":
            self.query_one("#events", RichLog).clear()

    def on_log_follow_changed(self, message: Log.FollowChanged) -> None:
        self._record_follow(message)

    def on_rich_log_follow_changed(self, message: RichLog.FollowChanged) -> None:
        self._record_follow(message)

    def _record_follow(self, message: Log.FollowChanged | RichLog.FollowChanged) -> None:
        if message.widget.id == "events":
            return
        self.query_one("#events", RichLog).write(
            "FollowChanged "
            f"following={message.is_following_end} "
            f"scroll_y={message.scroll_y} "
            f"max_scroll_y={message.max_scroll_y}"
        )


if __name__ == "__main__":
    RichLogFollowStateApp().run()

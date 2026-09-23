"""Show Log and RichLog follow-end state."""

from __future__ import annotations

from rich.text import Text

from textual.app import App, ComposeResult
from textual.containers import Horizontal, Vertical
from textual.widgets import Button, Log, RichLog


class RichLogFollowStateApp(App):
    """Buttons drive follow, append, expanded write, and the events log."""

    CSS = """
    #toolbar {
        height: auto;
        layout: grid;
        grid-size: 3 2;
        grid-gutter: 1;
    }
    #logs { height: 1fr; }
    Log, RichLog { height: 1fr; border: solid $primary; }
    Button { width: 1fr; }
    """

    def compose(self) -> ComposeResult:
        with Horizontal(id="toolbar"):
            yield Button("Follow log", id="follow-log")
            yield Button("Follow rich", id="follow-rich")
            yield Button("Write expanded", id="write-expanded")
            yield Button("Append log", id="append-log")
            yield Button("Append rich", id="append-rich")
            yield Button("Clear events", id="clear-events")
        with Vertical(id="logs"):
            yield Log(id="log")
            yield RichLog(id="rich-log")
            yield RichLog(id="events")

    def on_button_pressed(self, event: Button.Pressed) -> None:
        button_id = event.button.id
        if button_id == "follow-log":
            self.query_one("#log", Log).follow_end()
        elif button_id == "follow-rich":
            self.query_one("#rich-log", RichLog).follow_end()
        elif button_id == "write-expanded":
            self.query_one("#rich-log", RichLog).write(
                Text("expanded", style="on red", justify="right"),
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

    def _record_follow(
        self, message: Log.FollowChanged | RichLog.FollowChanged
    ) -> None:
        if message.widget.id == "events":
            return
        self.query_one("#events", RichLog).write(
            "FollowChanged "
            f"widget={message.widget.id} "
            f"is_following_end={message.is_following_end} "
            f"scroll_y={message.scroll_y} "
            f"max_scroll_y={message.max_scroll_y}"
        )


if __name__ == "__main__":
    RichLogFollowStateApp().run()

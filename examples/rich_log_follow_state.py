"""Exercise Log and RichLog follow state, including expanded writes."""

from __future__ import annotations

from rich.text import Text

from textual.app import App, ComposeResult
from textual.containers import Horizontal
from textual.widgets import Button, Log, RichLog


class RichLogFollowStateApp(App):
    """Buttons drive follow, append, expanded writes, and the events log."""

    CSS = """
    #buttons {
        height: auto;
    }
    #log, #rich-log, #events {
        height: 1fr;
        min-height: 5;
    }
    """

    def compose(self) -> ComposeResult:
        with Horizontal(id="buttons"):
            yield Button("Follow log", id="follow-log")
            yield Button("Follow rich", id="follow-rich")
            yield Button("Write expanded", id="write-expanded")
            yield Button("Append log", id="append-log")
            yield Button("Append rich", id="append-rich")
            yield Button("Clear events", id="clear-events")
        yield Log(id="log")
        yield RichLog(id="rich-log", min_width=20)
        yield RichLog(id="events")

    def _record_follow(self, event: Log.FollowChanged | RichLog.FollowChanged) -> None:
        """Append a line containing FollowChanged to the events log."""
        events = self.query_one("#events", RichLog)
        events.write(
            "FollowChanged "
            f"widget={event.widget.id} "
            f"is_following_end={event.is_following_end} "
            f"scroll_y={event.scroll_y} "
            f"max_scroll_y={event.max_scroll_y}"
        )

    def on_log_follow_changed(self, event: Log.FollowChanged) -> None:
        self._record_follow(event)

    def on_rich_log_follow_changed(self, event: RichLog.FollowChanged) -> None:
        if event.widget.id == "events":
            return
        self._record_follow(event)

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


if __name__ == "__main__":
    app = RichLogFollowStateApp()
    app.run()

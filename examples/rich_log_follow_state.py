"""Follow-end state for Log and RichLog."""

from __future__ import annotations

from rich.text import Text

from textual.app import App, ComposeResult
from textual.containers import Horizontal, Vertical
from textual.widgets import Button, Log, RichLog


class RichLogFollowStateApp(App):
    """Show Log and RichLog end-follow, including expanded writes."""

    CSS = """
    #controls { height: auto; }
    #logs { height: 1fr; }
    #log, #rich-log { width: 1fr; height: 1fr; }
    #events { height: 8; }
    """

    def compose(self) -> ComposeResult:
        with Horizontal(id="controls"):
            yield Button("Follow log", id="follow-log")
            yield Button("Follow rich", id="follow-rich")
            yield Button("Write expanded", id="write-expanded")
            yield Button("Append log", id="append-log")
            yield Button("Append rich", id="append-rich")
            yield Button("Clear events", id="clear-events")
        with Horizontal(id="logs"):
            yield Log(id="log", max_lines=200)
            yield RichLog(id="rich-log", max_lines=200)
        with Vertical():
            yield RichLog(id="events", max_lines=200)

    def on_mount(self) -> None:
        log = self.query_one("#log", Log)
        rich_log = self.query_one("#rich-log", RichLog)
        for index in range(40):
            log.write_line(f"log {index}")
            rich_log.write(f"rich {index}")

    def on_button_pressed(self, event: Button.Pressed) -> None:
        button_id = event.button.id
        log = self.query_one("#log", Log)
        rich_log = self.query_one("#rich-log", RichLog)
        events = self.query_one("#events", RichLog)
        if button_id == "follow-log":
            log.follow_end()
        elif button_id == "follow-rich":
            rich_log.follow_end()
        elif button_id == "write-expanded":
            rich_log.write(Text("expanded", justify="right"), expand=True)
        elif button_id == "append-log":
            log.write_line("log line")
        elif button_id == "append-rich":
            rich_log.write("rich line")
        elif button_id == "clear-events":
            events.clear()

    def on_log_follow_changed(self, event: Log.FollowChanged) -> None:
        self._record_follow(event)

    def on_rich_log_follow_changed(self, event: RichLog.FollowChanged) -> None:
        self._record_follow(event)

    def _record_follow(self, event: Log.FollowChanged | RichLog.FollowChanged) -> None:
        if event.widget.id == "events":
            return
        events = self.query_one("#events", RichLog)
        events.write(
            "FollowChanged "
            f"widget={event.widget.id} "
            f"following={event.is_following_end} "
            f"scroll_y={event.scroll_y} "
            f"max_scroll_y={event.max_scroll_y}"
        )


if __name__ == "__main__":
    RichLogFollowStateApp().run()

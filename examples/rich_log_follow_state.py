"""
A demonstration of the follow state of the Log and RichLog widgets.

Scroll up in either log to stop following new lines, then scroll back to the
end (or press a "Follow" button) to resume following.
"""

from __future__ import annotations

from rich.text import Text

from textual import on
from textual.app import App, ComposeResult
from textual.containers import Horizontal
from textual.widgets import Button, Footer, Log, RichLog


class RichLogFollowStateApp(App):
    CSS = """
    #logs {
        height: 1fr;
        Log, RichLog { width: 1fr; border: round $primary; }
    }
    #buttons {
        height: auto;
        Button { margin: 0 1; }
    }
    #events {
        height: 10;
        border: round $secondary;
    }
    """

    def compose(self) -> ComposeResult:
        with Horizontal(id="logs"):
            yield Log(id="log", max_lines=500)
            yield RichLog(id="rich", max_lines=500, min_width=20)
        with Horizontal(id="buttons"):
            yield Button("Follow Log", id="follow-log")
            yield Button("Follow RichLog", id="follow-rich")
            yield Button("Write expanded", id="write-expanded")
            yield Button("Append Log", id="append-log")
            yield Button("Append RichLog", id="append-rich")
            yield Button("Clear events", id="clear-events")
        yield RichLog(id="events", min_width=20)
        yield Footer()

    def on_mount(self) -> None:
        self._log_count = 0
        self._rich_count = 0
        log = self.query_one("#log", Log)
        rich_log = self.query_one("#rich", RichLog)
        log.border_title = "Log"
        rich_log.border_title = "RichLog"
        self.query_one("#events", RichLog).border_title = "Events"
        for _ in range(50):
            self._append_log_line(log)
            self._append_rich_line(rich_log)

    def _append_log_line(self, log: Log) -> None:
        self._log_count += 1
        log.write_line(f"Log line {self._log_count}")

    def _append_rich_line(self, rich_log: RichLog) -> None:
        self._rich_count += 1
        rich_log.write(f"RichLog line {self._rich_count}")

    def _record(
        self, name: str, message: Log.FollowChanged | RichLog.FollowChanged
    ) -> None:
        self.query_one("#events", RichLog).write(
            f"{name}.FollowChanged #{message.widget.id} "
            f"is_following_end={message.is_following_end} "
            f"scroll_y={message.scroll_y:g} max_scroll_y={message.max_scroll_y}"
        )

    def on_log_follow_changed(self, message: Log.FollowChanged) -> None:
        self._record("Log", message)

    def on_rich_log_follow_changed(self, message: RichLog.FollowChanged) -> None:
        if message.widget.id != "events":
            self._record("RichLog", message)

    @on(Button.Pressed, "#follow-log")
    def follow_log(self) -> None:
        self.query_one("#log", Log).follow_end()

    @on(Button.Pressed, "#follow-rich")
    def follow_rich(self) -> None:
        self.query_one("#rich", RichLog).follow_end()

    @on(Button.Pressed, "#write-expanded")
    def write_expanded(self) -> None:
        self.query_one("#rich", RichLog).write(
            Text("Expanded, right justified", style="reverse", justify="right"),
            expand=True,
        )

    @on(Button.Pressed, "#append-log")
    def append_log(self) -> None:
        self._append_log_line(self.query_one("#log", Log))

    @on(Button.Pressed, "#append-rich")
    def append_rich(self) -> None:
        self._append_rich_line(self.query_one("#rich", RichLog))

    @on(Button.Pressed, "#clear-events")
    def clear_events(self) -> None:
        self.query_one("#events", RichLog).clear()


if __name__ == "__main__":
    RichLogFollowStateApp().run()

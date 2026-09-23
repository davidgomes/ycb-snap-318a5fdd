"""Demonstrates the follow-the-end state of Log and RichLog."""

from __future__ import annotations

from rich.text import Text

from textual import on
from textual.app import App, ComposeResult
from textual.containers import Horizontal, Vertical
from textual.widgets import Button, Log, RichLog


class RichLogFollowStateApp(App):
    CSS = """
    #logs { height: 1fr; }
    #logs > * { width: 1fr; }
    #events { height: 8; }
    #buttons { height: auto; }
    """

    def compose(self) -> ComposeResult:
        with Horizontal(id="logs"):
            yield Log(id="log")
            yield RichLog(id="rich")
        yield RichLog(id="events")
        with Horizontal(id="buttons"):
            yield Button("Follow Log", id="follow-log")
            yield Button("Follow RichLog", id="follow-rich")
            yield Button("Write expanded", id="write-expanded")
            yield Button("Append Log", id="append-log")
            yield Button("Append RichLog", id="append-rich")
            yield Button("Clear events", id="clear-events")

    def on_mount(self) -> None:
        log = self.query_one("#log", Log)
        rich_log = self.query_one("#rich", RichLog)
        for index in range(100):
            log.write_line(f"Log line {index}")
            rich_log.write(f"RichLog line {index}")
        self._count = 100

    @on(Log.FollowChanged)
    @on(RichLog.FollowChanged)
    def record_follow_changed(
        self, event: Log.FollowChanged | RichLog.FollowChanged
    ) -> None:
        self.query_one("#events", RichLog).write(
            f"FollowChanged #{event.widget.id} "
            f"is_following_end={event.is_following_end} "
            f"scroll_y={event.scroll_y} max_scroll_y={event.max_scroll_y}"
        )

    @on(Button.Pressed, "#follow-log")
    def follow_log(self) -> None:
        self.query_one("#log", Log).follow_end()

    @on(Button.Pressed, "#follow-rich")
    def follow_rich(self) -> None:
        self.query_one("#rich", RichLog).follow_end()

    @on(Button.Pressed, "#write-expanded")
    def write_expanded(self) -> None:
        self.query_one("#rich", RichLog).write(
            Text("Expanded, right justified", justify="right", style="reverse"),
            expand=True,
        )

    @on(Button.Pressed, "#append-log")
    def append_log(self) -> None:
        self._count += 1
        self.query_one("#log", Log).write_line(f"Log line {self._count}")

    @on(Button.Pressed, "#append-rich")
    def append_rich(self) -> None:
        self._count += 1
        self.query_one("#rich", RichLog).write(f"RichLog line {self._count}")

    @on(Button.Pressed, "#clear-events")
    def clear_events(self) -> None:
        self.query_one("#events", RichLog).clear()


if __name__ == "__main__":
    RichLogFollowStateApp().run()

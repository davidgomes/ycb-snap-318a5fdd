"""
Demonstrates the follow-end state of the Log and RichLog widgets.

Scroll either log up to stop following new lines, and scroll back to the end
(or press a "Follow" button) to follow again. Changes are recorded in the
events log at the bottom.
"""

from __future__ import annotations

from itertools import count

from rich.text import Text

from textual import on
from textual.app import App, ComposeResult
from textual.containers import Horizontal
from textual.widgets import Button, Footer, Log, RichLog


class RichLogFollowStateApp(App[None]):
    CSS = """
    #logs {
        height: 1fr;
    }
    #logs > * {
        width: 1fr;
        border: round $primary;
    }
    #buttons {
        height: auto;
    }
    #events {
        height: 8;
        border: round $secondary;
    }
    """

    def compose(self) -> ComposeResult:
        with Horizontal(id="logs"):
            yield Log(id="log")
            yield RichLog(id="rich", min_width=20)
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
        self._counter = count(1)
        self.query_one("#log", Log).border_title = "Log"
        self.query_one("#rich", RichLog).border_title = "RichLog"
        self.query_one("#events", RichLog).border_title = "Events"
        log = self.query_one("#log", Log)
        rich_log = self.query_one("#rich", RichLog)
        for _ in range(50):
            log.write_line(f"Log line {next(self._counter)}")
            rich_log.write(f"RichLog line {next(self._counter)}")

    @on(Button.Pressed, "#follow-log")
    def follow_log(self) -> None:
        self.query_one("#log", Log).follow_end()

    @on(Button.Pressed, "#follow-rich")
    def follow_rich(self) -> None:
        self.query_one("#rich", RichLog).follow_end()

    @on(Button.Pressed, "#write-expanded")
    def write_expanded(self) -> None:
        self.query_one("#rich", RichLog).write(
            Text(
                f"Expanded entry {next(self._counter)}",
                style="reverse",
                justify="right",
            ),
            expand=True,
        )

    @on(Button.Pressed, "#append-log")
    def append_log(self) -> None:
        self.query_one("#log", Log).write_line(f"Log line {next(self._counter)}")

    @on(Button.Pressed, "#append-rich")
    def append_rich(self) -> None:
        self.query_one("#rich", RichLog).write(f"RichLog line {next(self._counter)}")

    @on(Button.Pressed, "#clear-events")
    def clear_events(self) -> None:
        self.query_one("#events", RichLog).clear()

    def on_log_follow_changed(self, event: Log.FollowChanged) -> None:
        self._record_follow_changed(event)

    def on_rich_log_follow_changed(self, event: RichLog.FollowChanged) -> None:
        self._record_follow_changed(event)

    def _record_follow_changed(
        self, event: Log.FollowChanged | RichLog.FollowChanged
    ) -> None:
        if event.widget.id == "events":
            return
        self.query_one("#events", RichLog).write(
            f"FollowChanged widget=#{event.widget.id} "
            f"is_following_end={event.is_following_end} "
            f"scroll_y={event.scroll_y:g} max_scroll_y={event.max_scroll_y}"
        )


if __name__ == "__main__":
    RichLogFollowStateApp().run()

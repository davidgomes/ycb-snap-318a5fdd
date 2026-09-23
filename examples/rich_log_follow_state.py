"""
A demonstration of how `Log` and `RichLog` follow the end of their content.

Scroll either log up and it will stop following new lines. Scroll back to the end,
or press one of the "Follow" buttons, to resume following. Every change to the
follow state is recorded in the events log at the bottom.
"""

from rich.text import Text

from textual import on
from textual.app import App, ComposeResult
from textual.containers import Grid, Horizontal
from textual.widgets import Button, Log, RichLog


class RichLogFollowStateApp(App):
    log_line_count = 0
    rich_line_count = 0
    expanded_count = 0

    CSS = """
    Grid {
        grid-size: 3;
        grid-gutter: 0 1;
        height: auto;
        Button { width: 1fr; }
    }
    #logs {
        height: 1fr;
        Log, RichLog { width: 1fr; border: round $primary; }
    }
    #events-panel {
        height: 8;
        RichLog { border: round $secondary; }
    }
    """

    def compose(self) -> ComposeResult:
        with Grid():
            yield Button("Follow Log", id="follow-log")
            yield Button("Follow RichLog", id="follow-rich")
            yield Button("Write expanded", id="write-expanded")
            yield Button("Append Log", id="append-log")
            yield Button("Append RichLog", id="append-rich")
            yield Button("Clear events", id="clear-events")
        with Horizontal(id="logs"):
            yield Log(id="log")
            yield RichLog(id="rich-log", min_width=20)
        with Horizontal(id="events-panel"):
            yield RichLog(id="events")

    def on_mount(self) -> None:
        self.query_one("#log", Log).border_title = "Log"
        self.query_one("#rich-log", RichLog).border_title = "RichLog"
        self.query_one("#events", RichLog).border_title = "Events"
        for _ in range(30):
            self.append_log_line()
            self.append_rich_line()

    def append_log_line(self) -> None:
        self.log_line_count += 1
        self.query_one("#log", Log).write_line(f"Log line {self.log_line_count}")

    def append_rich_line(self) -> None:
        self.rich_line_count += 1
        self.query_one("#rich-log", RichLog).write(
            f"RichLog line {self.rich_line_count}"
        )

    @on(Log.FollowChanged)
    @on(RichLog.FollowChanged)
    def record_follow_changed(
        self, event: Log.FollowChanged | RichLog.FollowChanged
    ) -> None:
        events = self.query_one("#events", RichLog)
        if event.widget is events:
            return
        events.write(
            f"FollowChanged #{event.widget.id}: "
            f"is_following_end={event.is_following_end} "
            f"scroll_y={event.scroll_y:g} max_scroll_y={event.max_scroll_y}"
        )

    @on(Button.Pressed, "#follow-log")
    def follow_log(self) -> None:
        self.query_one("#log", Log).follow_end()

    @on(Button.Pressed, "#follow-rich")
    def follow_rich(self) -> None:
        self.query_one("#rich-log", RichLog).follow_end()

    @on(Button.Pressed, "#write-expanded")
    def write_expanded(self) -> None:
        self.expanded_count += 1
        self.query_one("#rich-log", RichLog).write(
            Text(
                f"Expanded entry {self.expanded_count}",
                style="bold reverse",
                justify="center",
            ),
            expand=True,
        )

    @on(Button.Pressed, "#append-log")
    def append_log(self) -> None:
        self.append_log_line()

    @on(Button.Pressed, "#append-rich")
    def append_rich(self) -> None:
        self.append_rich_line()

    @on(Button.Pressed, "#clear-events")
    def clear_events(self) -> None:
        self.query_one("#events", RichLog).clear()


if __name__ == "__main__":
    RichLogFollowStateApp().run()

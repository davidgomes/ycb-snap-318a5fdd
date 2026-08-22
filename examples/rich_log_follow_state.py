from rich.text import Text

from textual.app import App, ComposeResult
from textual.widgets import Button, Log, RichLog


class RichLogFollowStateApp(App[None]):
    """Demonstrate follow state and viewport-preserving log writes."""

    def compose(self) -> ComposeResult:
        yield Button("Follow Log", id="follow-log")
        yield Button("Follow RichLog", id="follow-rich")
        yield Button("Write Expanded", id="write-expanded")
        yield Button("Append Log", id="append-log")
        yield Button("Append RichLog", id="append-rich")
        yield Button("Clear Events", id="clear-events")
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
                Text("Expanded entry", justify="center"), expand=True
            )
        elif button_id == "append-log":
            self.query_one("#log", Log).write_line("ordinary log entry")
        elif button_id == "append-rich":
            self.query_one("#rich-log", RichLog).write("ordinary RichLog entry")
        elif button_id == "clear-events":
            self.query_one("#events", RichLog).clear()

    def on_log_follow_changed(self, message: Log.FollowChanged) -> None:
        self._record_follow_changed(message)

    def on_rich_log_follow_changed(self, message: RichLog.FollowChanged) -> None:
        self._record_follow_changed(message)

    def _record_follow_changed(self, message: Log.FollowChanged | RichLog.FollowChanged):
        self.query_one("#events", RichLog).write(
            f"FollowChanged: {message.widget.__class__.__name__} "
            f"is_following_end={message.is_following_end} "
            f"scroll_y={message.scroll_y} max_scroll_y={message.max_scroll_y}"
        )


if __name__ == "__main__":
    RichLogFollowStateApp().run()

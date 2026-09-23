from __future__ import annotations

from rich.text import Text

from textual.app import App, ComposeResult
from textual.widgets import RichLog


async def test_make_renderable_expand_tabs():
    # Regression test for https://github.com/Textualize/textual/issues/3007
    text_log = RichLog()
    renderable = text_log._make_renderable("\tfoo")
    assert isinstance(renderable, Text)
    assert renderable.plain == "        foo"


class FollowRichLogApp(App):
    CSS = "RichLog { height: 10; }"

    def __init__(self, max_lines: int | None = None) -> None:
        super().__init__()
        self.max_lines = max_lines
        self.messages: list[RichLog.FollowChanged] = []

    def compose(self) -> ComposeResult:
        yield RichLog(max_lines=self.max_lines, min_width=10)

    def on_mount(self) -> None:
        rich_log = self.query_one(RichLog)
        for n in range(50):
            rich_log.write(f"line {n}")

    def on_rich_log_follow_changed(self, message: RichLog.FollowChanged) -> None:
        self.messages.append(message)


def line_text(rich_log: RichLog, y: int) -> str:
    return rich_log.lines[y].text.rstrip()


async def test_rich_log_follow_state() -> None:
    app = FollowRichLogApp()
    async with app.run_test() as pilot:
        rich_log = app.query_one(RichLog)
        await pilot.pause()
        assert rich_log.is_following_end
        assert rich_log.scroll_y == rich_log.max_scroll_y > 0

        rich_log.scroll_to(y=5, animate=False)
        await pilot.pause()
        assert not rich_log.is_following_end
        assert rich_log.scroll_offset.y == 5
        assert rich_log.vertical_scrollbar.position == 5

        rich_log.write("new line")
        await pilot.pause()
        assert rich_log.scroll_offset.y == 5
        assert not rich_log.is_following_end

        rich_log.scroll_to(y=rich_log.max_scroll_y, animate=False)
        await pilot.pause()
        assert rich_log.is_following_end
        rich_log.write("another line")
        await pilot.pause()
        assert rich_log.scroll_y == rich_log.max_scroll_y

        rich_log.scroll_to(y=0, animate=False)
        await pilot.pause()
        rich_log.follow_end()
        await pilot.pause()
        assert rich_log.is_following_end
        assert rich_log.scroll_y == rich_log.max_scroll_y

        assert [message.is_following_end for message in app.messages] == [
            False,
            True,
            False,
            True,
        ]
        first = app.messages[0]
        assert first.widget is rich_log
        assert first.control is rich_log
        assert first.scroll_y == 5
        assert first.max_scroll_y == 40


async def test_rich_log_pending_scroll_does_not_snap_back() -> None:
    app = FollowRichLogApp()
    async with app.run_test() as pilot:
        rich_log = app.query_one(RichLog)
        await pilot.pause()
        rich_log.write("new line")
        rich_log.scroll_to(y=5, animate=False)
        await pilot.pause()
        assert rich_log.scroll_offset.y == 5
        assert not rich_log.is_following_end


async def test_rich_log_prune_keeps_viewport() -> None:
    app = FollowRichLogApp(max_lines=50)
    async with app.run_test() as pilot:
        rich_log = app.query_one(RichLog)
        await pilot.pause()
        rich_log.scroll_to(y=20, animate=False)
        await pilot.pause()
        assert line_text(rich_log, rich_log.scroll_offset.y) == "line 20"

        for line in ["a", "b", "c"]:
            rich_log.write(line)
        await pilot.pause()
        assert rich_log.scroll_offset.y == 17
        assert line_text(rich_log, rich_log.scroll_offset.y) == "line 20"
        assert not rich_log.is_following_end


class ExpandApp(App):
    CSS = "RichLog { scrollbar-size-vertical: 0; }"

    def compose(self) -> ComposeResult:
        rich_log = RichLog(min_width=10)
        rich_log.write(Text("deferred", justify="right"), expand=True)
        yield rich_log

    def on_mount(self) -> None:
        rich_log = self.query_one(RichLog)
        rich_log.write(Text("left", justify="left"), expand=True)


async def test_rich_log_write_expand_justify() -> None:
    app = ExpandApp()
    async with app.run_test(size=(40, 10)) as pilot:
        rich_log = app.query_one(RichLog)
        await pilot.pause()
        rich_log.write(Text("center", justify="center"), expand=True)
        rich_log.write(Text("right", justify="right"), expand=True)
        rich_log.write(Text("not expanded", justify="right"))
        await pilot.pause()
        assert [line.text for line in rich_log.lines] == [
            " " * 32 + "deferred",
            "left",
            " " * 17 + "center" + " " * 17,
            " " * 35 + "right",
            "not expanded",
        ]

        await pilot.resize_terminal(30, 10)
        await pilot.pause()
        assert [line.text for line in rich_log.lines[:4]] == [
            " " * 22 + "deferred",
            "left",
            " " * 12 + "center" + " " * 12,
            " " * 25 + "right",
        ]
        assert rich_log.virtual_size.width == 30

        rich_log.min_width = 36
        await pilot.pause()
        assert [line.text for line in rich_log.lines[:4]] == [
            " " * 28 + "deferred",
            "left",
            " " * 15 + "center" + " " * 15,
            " " * 31 + "right",
        ]

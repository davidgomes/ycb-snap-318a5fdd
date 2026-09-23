from __future__ import annotations

import pytest
from rich.text import Text

from textual.app import App, ComposeResult
from textual.widgets import Log, RichLog


class FollowApp(App[None]):
    CSS = """
    Log, RichLog { height: 10; width: 40; }
    """

    def __init__(self, widget_type: type[Log] | type[RichLog], **kwargs) -> None:
        super().__init__()
        self.widget_type = widget_type
        self.widget_kwargs = kwargs
        self.messages: list[Log.FollowChanged | RichLog.FollowChanged] = []

    def compose(self) -> ComposeResult:
        yield self.widget_type(**self.widget_kwargs)

    def on_log_follow_changed(self, message: Log.FollowChanged) -> None:
        self.messages.append(message)

    def on_rich_log_follow_changed(self, message: RichLog.FollowChanged) -> None:
        self.messages.append(message)


def write_line(widget: Log | RichLog, line: str) -> None:
    if isinstance(widget, Log):
        widget.write_line(line)
    else:
        widget.write(line)


WIDGET_TYPES = [Log, RichLog]


@pytest.mark.parametrize("widget_type", WIDGET_TYPES)
async def test_scroll_up_stops_following_and_end_restores(widget_type) -> None:
    app = FollowApp(widget_type)
    async with app.run_test() as pilot:
        widget = app.query_one(widget_type)
        for n in range(50):
            write_line(widget, f"line {n}")
        await pilot.pause()
        assert widget.is_following_end
        assert widget.scroll_y == widget.max_scroll_y > 0
        assert app.messages == []

        widget.scroll_to(y=5, animate=False)
        await pilot.pause()
        assert not widget.is_following_end
        assert widget.vertical_scrollbar.position == 5
        assert len(app.messages) == 1
        message = app.messages[0]
        assert message.widget is widget
        assert message.control is widget
        assert message.is_following_end is False
        assert message.scroll_y == 5
        assert message.max_scroll_y == widget.max_scroll_y

        # Further scrolling that doesn't reach the end doesn't post again.
        widget.scroll_to(y=10, animate=False)
        await pilot.pause()
        assert len(app.messages) == 1

        # Writes don't snap back to the end when not following.
        for n in range(10):
            write_line(widget, f"more {n}")
        await pilot.pause()
        assert widget.scroll_y == 10
        assert not widget.is_following_end

        # Scrolling back to the end restores follow.
        widget.scroll_end(animate=False, immediate=True)
        await pilot.pause()
        assert widget.is_following_end
        assert len(app.messages) == 2
        assert app.messages[1].is_following_end is True

        write_line(widget, "followed")
        await pilot.pause()
        assert widget.scroll_y == widget.max_scroll_y
        assert len(app.messages) == 2


@pytest.mark.parametrize("widget_type", WIDGET_TYPES)
async def test_key_scrolling(widget_type) -> None:
    app = FollowApp(widget_type)
    async with app.run_test() as pilot:
        widget = app.query_one(widget_type)
        widget.focus()
        for n in range(50):
            write_line(widget, f"line {n}")
        await pilot.pause()
        max_scroll_y = widget.max_scroll_y

        await pilot.press("pageup")
        await pilot.wait_for_scheduled_animations()
        await pilot.pause()
        assert widget.scroll_y < max_scroll_y
        assert widget.vertical_scrollbar.position == widget.scroll_y
        assert not widget.is_following_end
        scroll_y = widget.scroll_y

        write_line(widget, "no snap")
        await pilot.pause()
        assert widget.scroll_y == scroll_y

        await pilot.press("end")
        await pilot.wait_for_scheduled_animations()
        await pilot.pause()
        assert widget.is_following_end
        assert [message.is_following_end for message in app.messages] == [
            False,
            True,
        ]


@pytest.mark.parametrize("widget_type", WIDGET_TYPES)
async def test_follow_end(widget_type) -> None:
    app = FollowApp(widget_type)
    async with app.run_test() as pilot:
        widget = app.query_one(widget_type)
        for n in range(50):
            write_line(widget, f"line {n}")
        await pilot.pause()
        widget.scroll_home(animate=False, immediate=True)
        await pilot.pause()
        assert not widget.is_following_end

        widget.follow_end()
        await pilot.pause()
        assert widget.is_following_end
        assert widget.scroll_y == widget.max_scroll_y
        assert [message.is_following_end for message in app.messages] == [
            False,
            True,
        ]
        assert app.messages[1].scroll_y == widget.max_scroll_y

        # Already following, so no message.
        widget.follow_end()
        await pilot.pause()
        assert len(app.messages) == 2


@pytest.mark.parametrize("widget_type", WIDGET_TYPES)
async def test_follow_end_animated(widget_type) -> None:
    app = FollowApp(widget_type)
    async with app.run_test() as pilot:
        widget = app.query_one(widget_type)
        for n in range(50):
            write_line(widget, f"line {n}")
        await pilot.pause()
        widget.scroll_home(animate=False, immediate=True)
        await pilot.pause()

        widget.follow_end(animate=True)
        await pilot.wait_for_scheduled_animations()
        await pilot.pause()
        assert widget.is_following_end
        assert widget.scroll_y == widget.max_scroll_y
        assert [message.is_following_end for message in app.messages] == [
            False,
            True,
        ]


@pytest.mark.parametrize("widget_type", WIDGET_TYPES)
async def test_max_lines_keeps_viewport_when_not_following(widget_type) -> None:
    app = FollowApp(widget_type, max_lines=30)
    async with app.run_test() as pilot:
        widget = app.query_one(widget_type)
        for n in range(30):
            write_line(widget, f"line {n}")
        await pilot.pause()
        widget.scroll_to(y=10, animate=False)
        await pilot.pause()
        assert not widget.is_following_end
        top_line = widget.render_line(0).text

        for n in range(5):
            write_line(widget, f"more {n}")
        await pilot.pause()
        assert widget.scroll_y == 5
        assert widget.render_line(0).text == top_line
        assert not widget.is_following_end


@pytest.mark.parametrize("widget_type", WIDGET_TYPES)
async def test_auto_scroll_disabled(widget_type) -> None:
    app = FollowApp(widget_type, auto_scroll=False)
    async with app.run_test() as pilot:
        widget = app.query_one(widget_type)
        for n in range(50):
            write_line(widget, f"line {n}")
        await pilot.pause()
        assert widget.scroll_y == 0
        assert not widget.is_following_end
        assert len(app.messages) == 1


@pytest.mark.parametrize("widget_type", WIDGET_TYPES)
async def test_clear_restores_following(widget_type) -> None:
    app = FollowApp(widget_type)
    async with app.run_test() as pilot:
        widget = app.query_one(widget_type)
        for n in range(50):
            write_line(widget, f"line {n}")
        await pilot.pause()
        widget.scroll_home(animate=False, immediate=True)
        await pilot.pause()
        assert not widget.is_following_end
        widget.clear()
        await pilot.pause()
        assert widget.is_following_end


def rendered_rows(rich_log: RichLog) -> list[str]:
    return [line.text for line in rich_log.lines]


async def test_rich_log_expand_justify_explicit_write() -> None:
    app = FollowApp(RichLog, min_width=10)
    async with app.run_test() as pilot:
        rich_log = app.query_one(RichLog)
        await pilot.pause()
        content_width = rich_log.scrollable_content_region.width
        rich_log.write(Text("right", justify="right"), expand=True)
        rich_log.write(Text("center", justify="center"), expand=True)
        rich_log.write(Text("left", justify="right"))
        long_line = "x" * (content_width * 2)
        rich_log.write(Text(long_line, justify="right"), expand=True)
        await pilot.pause()
        right, center, left, long = rendered_rows(rich_log)
        assert right == "right".rjust(content_width)
        assert center.rstrip() == "center".center(content_width).rstrip()
        assert left == "left".rjust(10)
        assert long == long_line


async def test_rich_log_expand_deferred_write() -> None:
    class DeferredApp(App[None]):
        CSS = "RichLog { width: 30; }"

        def compose(self) -> ComposeResult:
            rich_log = RichLog(min_width=10)
            rich_log.write(Text("deferred", justify="center"), expand=True)
            yield rich_log

    app = DeferredApp()
    async with app.run_test() as pilot:
        await pilot.pause()
        rich_log = app.query_one(RichLog)
        content_width = rich_log.scrollable_content_region.width
        assert rendered_rows(rich_log) == ["deferred".center(content_width)]


async def test_rich_log_expand_rerenders_on_resize_and_min_width() -> None:
    class ResizeApp(App[None]):
        CSS = "RichLog { width: 1fr; }"

        def compose(self) -> ComposeResult:
            yield RichLog(min_width=10)

    app = ResizeApp()
    async with app.run_test(size=(40, 10)) as pilot:
        rich_log = app.query_one(RichLog)
        await pilot.pause()
        rich_log.write(Text("expanded", justify="right"), expand=True)
        rich_log.write(Text("fixed", justify="right"))
        await pilot.pause()
        width = rich_log.scrollable_content_region.width
        assert rendered_rows(rich_log) == ["expanded".rjust(width), "fixed".rjust(10)]

        await pilot.resize_terminal(60, 10)
        await pilot.pause()
        new_width = rich_log.scrollable_content_region.width
        assert new_width > width
        assert rendered_rows(rich_log) == [
            "expanded".rjust(new_width),
            "fixed".rjust(10),
        ]

        rich_log.min_width = 80
        await pilot.pause()
        assert rendered_rows(rich_log)[0] == "expanded".rjust(80)
        assert rich_log.virtual_size.width == 80

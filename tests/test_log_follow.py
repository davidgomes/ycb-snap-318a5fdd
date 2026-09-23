from __future__ import annotations

import importlib.util
from pathlib import Path

import pytest

from textual.app import App, ComposeResult
from textual.widgets import Button, Log, RichLog

EXAMPLES_DIR = Path(__file__).parent.parent / "examples"


class FollowApp(App[None]):
    CSS = "Log, RichLog { height: 10; }"

    def __init__(self, log_widget: Log | RichLog) -> None:
        super().__init__()
        self.log_widget = log_widget
        self.follow_changes: list[Log.FollowChanged | RichLog.FollowChanged] = []

    def compose(self) -> ComposeResult:
        yield self.log_widget

    def on_log_follow_changed(self, event: Log.FollowChanged) -> None:
        self.follow_changes.append(event)

    def on_rich_log_follow_changed(self, event: RichLog.FollowChanged) -> None:
        self.follow_changes.append(event)


def write_lines(log_widget: Log | RichLog, start: int, count: int) -> None:
    for n in range(start, start + count):
        if isinstance(log_widget, Log):
            log_widget.write_line(f"line {n}")
        else:
            log_widget.write(f"line {n}")


def first_visible_line(log_widget: Log | RichLog) -> str:
    return log_widget.render_line(0).text.strip()


LOG_TYPES = pytest.mark.parametrize("log_type", [Log, RichLog])


@LOG_TYPES
async def test_follow_state_changes_when_scrolling(log_type: type[Log | RichLog]):
    log_widget = log_type()
    app = FollowApp(log_widget)
    async with app.run_test() as pilot:
        write_lines(log_widget, 0, 50)
        await pilot.pause()
        assert log_widget.is_following_end
        assert log_widget.scroll_y == log_widget.max_scroll_y
        assert app.follow_changes == []

        log_widget.scroll_home(animate=False)
        await pilot.pause()
        assert not log_widget.is_following_end
        assert len(app.follow_changes) == 1
        change = app.follow_changes[0]
        assert isinstance(change, log_type.FollowChanged)
        assert change.widget is log_widget
        assert change.control is log_widget
        assert change.is_following_end is False
        assert change.scroll_y == 0
        assert change.max_scroll_y == log_widget.max_scroll_y

        write_lines(log_widget, 50, 5)
        await pilot.pause()
        assert log_widget.scroll_y == 0
        assert first_visible_line(log_widget) == "line 0"
        assert not log_widget.is_following_end

        log_widget.scroll_end(animate=False)
        await pilot.pause()
        assert log_widget.is_following_end
        assert len(app.follow_changes) == 2
        assert app.follow_changes[1].is_following_end is True

        write_lines(log_widget, 55, 5)
        await pilot.pause()
        assert log_widget.scroll_y == log_widget.max_scroll_y
        assert (
            first_visible_line(log_widget)
            == f"line {60 - log_widget.scrollable_content_region.height}"
        )
        assert len(app.follow_changes) == 2


@LOG_TYPES
async def test_follow_end(log_type: type[Log | RichLog]):
    log_widget = log_type()
    app = FollowApp(log_widget)
    async with app.run_test() as pilot:
        write_lines(log_widget, 0, 50)
        await pilot.pause()
        log_widget.follow_end()
        await pilot.pause()
        assert app.follow_changes == []

        log_widget.scroll_to(y=10, animate=False)
        await pilot.pause()
        assert not log_widget.is_following_end

        log_widget.follow_end()
        assert log_widget.is_following_end
        await pilot.pause()
        assert log_widget.scroll_y == log_widget.max_scroll_y
        assert [change.is_following_end for change in app.follow_changes] == [
            False,
            True,
        ]
        assert app.follow_changes[1].scroll_y == log_widget.max_scroll_y

        write_lines(log_widget, 50, 5)
        await pilot.pause()
        assert log_widget.scroll_y == log_widget.max_scroll_y


@LOG_TYPES
async def test_write_during_scroll_up_does_not_snap_back(
    log_type: type[Log | RichLog],
):
    log_widget = log_type()
    app = FollowApp(log_widget)
    async with app.run_test() as pilot:
        write_lines(log_widget, 0, 50)
        await pilot.pause()
        log_widget.focus()
        await pilot.press("up")
        # Written before the (animated) scroll has moved the log.
        write_lines(log_widget, 50, 1)
        await pilot.pause()
        await pilot.wait_for_scheduled_animations()
        await pilot.pause()
        assert not log_widget.is_following_end
        assert log_widget.scroll_y < log_widget.max_scroll_y

        scroll_y = log_widget.scroll_y
        write_lines(log_widget, 51, 5)
        await pilot.pause()
        assert log_widget.scroll_y == scroll_y
        assert [change.is_following_end for change in app.follow_changes] == [False]


@LOG_TYPES
async def test_scrolling_updates_viewport_and_scrollbar(log_type: type[Log | RichLog]):
    log_widget = log_type()
    app = FollowApp(log_widget)
    async with app.run_test() as pilot:
        write_lines(log_widget, 0, 50)
        await pilot.pause()

        log_widget.scroll_to(y=5, animate=False)
        await pilot.pause()
        assert log_widget.scroll_offset.y == 5
        assert log_widget.vertical_scrollbar.position == 5
        assert first_visible_line(log_widget) == "line 5"

        log_widget.focus()
        await pilot.press("down")
        await pilot.wait_for_scheduled_animations()
        await pilot.pause()
        assert log_widget.scroll_offset.y == 6
        assert log_widget.vertical_scrollbar.position == 6
        assert first_visible_line(log_widget) == "line 6"
        assert not log_widget.is_following_end


@LOG_TYPES
async def test_max_lines_keeps_viewport_stable(log_type: type[Log | RichLog]):
    log_widget = log_type(max_lines=50)
    app = FollowApp(log_widget)
    async with app.run_test() as pilot:
        write_lines(log_widget, 0, 50)
        await pilot.pause()
        log_widget.scroll_to(y=20, animate=False)
        await pilot.pause()
        assert first_visible_line(log_widget) == "line 20"

        write_lines(log_widget, 50, 5)
        await pilot.pause()
        assert log_widget.scroll_y == 15
        assert log_widget.vertical_scrollbar.position == 15
        assert first_visible_line(log_widget) == "line 20"
        assert not log_widget.is_following_end


@LOG_TYPES
async def test_max_lines_while_following(log_type: type[Log | RichLog]):
    log_widget = log_type(max_lines=50)
    app = FollowApp(log_widget)
    async with app.run_test() as pilot:
        write_lines(log_widget, 0, 60)
        await pilot.pause()
        assert log_widget.is_following_end
        assert log_widget.scroll_y == log_widget.max_scroll_y
        height = log_widget.scrollable_content_region.height
        assert first_visible_line(log_widget) == f"line {60 - height}"


@LOG_TYPES
async def test_auto_scroll_disabled(log_type: type[Log | RichLog]):
    log_widget = log_type(auto_scroll=False)
    app = FollowApp(log_widget)
    async with app.run_test() as pilot:
        write_lines(log_widget, 0, 50)
        await pilot.pause()
        assert log_widget.scroll_y == 0
        assert not log_widget.is_following_end

        log_widget.follow_end()
        await pilot.pause()
        assert log_widget.is_following_end
        write_lines(log_widget, 50, 5)
        await pilot.pause()
        assert log_widget.scroll_y < log_widget.max_scroll_y
        assert not log_widget.is_following_end


@LOG_TYPES
async def test_clear_restores_follow(log_type: type[Log | RichLog]):
    log_widget = log_type()
    app = FollowApp(log_widget)
    async with app.run_test() as pilot:
        write_lines(log_widget, 0, 50)
        await pilot.pause()
        log_widget.scroll_home(animate=False)
        await pilot.pause()
        assert not log_widget.is_following_end

        log_widget.clear()
        assert log_widget.is_following_end
        write_lines(log_widget, 0, 50)
        await pilot.pause()
        assert log_widget.scroll_y == log_widget.max_scroll_y


def load_follow_state_example() -> type[App]:
    spec = importlib.util.spec_from_file_location(
        "rich_log_follow_state", EXAMPLES_DIR / "rich_log_follow_state.py"
    )
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.RichLogFollowStateApp


async def test_follow_state_example():
    app = load_follow_state_example()()
    async with app.run_test() as pilot:
        await pilot.pause()
        for button_id in (
            "follow-log",
            "follow-rich",
            "write-expanded",
            "append-log",
            "append-rich",
            "clear-events",
        ):
            assert isinstance(app.query_one(f"#{button_id}"), Button)

        log_widget = app.query_one(Log)
        rich_log = app.query_one("#rich-log", RichLog)
        events = app.query_one("#events", RichLog)
        assert app.query_one(RichLog) is rich_log
        assert app.query(RichLog).first() is rich_log

        log_widget.scroll_home(animate=False)
        rich_log.scroll_home(animate=False)
        await pilot.pause()
        assert any("FollowChanged" in line.text for line in events.lines)

        line_count = log_widget.line_count
        await pilot.click("#append-log")
        assert log_widget.line_count == line_count + 1
        assert log_widget.scroll_y == 0

        rich_line_count = len(rich_log.lines)
        await pilot.click("#append-rich")
        await pilot.pause()
        assert len(rich_log.lines) == rich_line_count + 1
        assert rich_log.scroll_y == 0

        await pilot.click("#clear-events")
        await pilot.pause()
        assert events.lines == []

        await pilot.click("#follow-log")
        await pilot.pause()
        assert log_widget.is_following_end
        assert log_widget.scroll_y == log_widget.max_scroll_y

        await pilot.click("#follow-rich")
        await pilot.pause()
        assert rich_log.is_following_end
        assert rich_log.scroll_y == rich_log.max_scroll_y
        assert sum("FollowChanged" in line.text for line in events.lines) == 2

        await pilot.click("#write-expanded")
        await pilot.pause()
        expanded = rich_log.lines[-1].text
        assert expanded.strip() == "Expanded entry 1"
        assert len(expanded) == rich_log.scrollable_content_region.width

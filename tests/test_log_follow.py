"""Follow state for Log and RichLog, and expanded RichLog rendering."""

from __future__ import annotations

import importlib.util
from pathlib import Path

from rich.text import Text

from textual.app import App, ComposeResult
from textual.widgets import Log, RichLog


def _plain(rich_log: RichLog, index: int = 0) -> str:
    return "".join(segment.text for segment in rich_log.lines[index]._segments)


async def test_log_follow_state_survives_appends_and_prunes() -> None:
    messages: list[tuple[bool, float, int]] = []

    class LogFollowApp(App):
        def compose(self) -> ComposeResult:
            yield Log(id="log", max_lines=30)

        def on_log_follow_changed(self, event: Log.FollowChanged) -> None:
            messages.append(
                (event.is_following_end, event.scroll_y, event.max_scroll_y)
            )

    async with LogFollowApp().run_test(size=(80, 10)) as pilot:
        log = pilot.app.query_one(Log)
        assert log.is_following_end is True
        for index in range(40):
            log.write_line(f"L{index}")
        await pilot.pause()
        assert log.is_following_end is True
        assert log.is_vertical_scroll_end
        assert log.vertical_scrollbar.position == log.scroll_y

        log.scroll_to(y=12, animate=False, immediate=True)
        await pilot.pause()
        assert log.is_following_end is False
        assert messages[-1][0] is False
        assert messages[-1][1] == log.scroll_y
        assert messages[-1][2] == log.max_scroll_y
        posted = len(messages)

        log.scroll_to(y=12, animate=False, immediate=True)
        await pilot.pause()
        assert len(messages) == posted

        top = log.lines[int(log.scroll_y)]
        scroll_y = log.scroll_y
        log.write_line("extra")
        await pilot.pause()
        assert int(log.scroll_y) == int(scroll_y) - 1
        assert log.lines[int(log.scroll_y)] == top
        assert log.is_following_end is False

        before = log.lines[int(log.scroll_y)]
        before_y = int(log.scroll_y)
        log.write_lines([f"N{index}" for index in range(5)])
        await pilot.pause()
        assert log.lines[int(log.scroll_y)] == before
        assert int(log.scroll_y) == before_y - 5
        assert log.vertical_scrollbar.position == log.scroll_y

        log.follow_end()
        await pilot.pause()
        assert log.is_following_end is True
        assert log.is_vertical_scroll_end
        assert messages[-1][0] is True
        posted = len(messages)
        log.follow_end()
        await pilot.pause()
        assert len(messages) == posted

        log.write_line("tail")
        await pilot.pause()
        assert log.is_vertical_scroll_end
        assert log.is_following_end is True


async def test_rich_log_does_not_snap_back_and_restores_follow() -> None:
    messages: list[bool] = []

    class RichFollowApp(App):
        def compose(self) -> ComposeResult:
            yield RichLog(id="rich")

        def on_rich_log_follow_changed(self, event: RichLog.FollowChanged) -> None:
            assert event.widget is self.query_one(RichLog)
            messages.append(event.is_following_end)

    async with RichFollowApp().run_test(size=(80, 10)) as pilot:
        rich = pilot.app.query_one(RichLog)
        for index in range(40):
            rich.write(f"R{index}")
        await pilot.pause()
        assert rich.is_following_end is True
        assert rich.is_vertical_scroll_end

        rich.scroll_to(y=2, animate=False, immediate=True)
        await pilot.pause()
        assert rich.is_following_end is False
        assert messages == [False]
        held = rich.scroll_y
        scrollbar = rich.vertical_scrollbar.position
        rich.write("should-stay")
        await pilot.pause()
        assert rich.scroll_y == held
        assert rich.vertical_scrollbar.position == scrollbar
        assert _plain(rich, int(rich.scroll_y)).strip() == "R2"

        rich.scroll_end(animate=False, immediate=True, x_axis=False)
        await pilot.pause()
        assert rich.is_following_end is True
        assert messages == [False, True]
        rich.write("at-end")
        await pilot.pause()
        assert rich.is_vertical_scroll_end
        assert _plain(rich, -1).strip() == "at-end"


async def test_rich_log_expand_justify_deferred_resize_and_min_width() -> None:
    class ExpandApp(App):
        CSS = """
        RichLog {
            width: 1fr;
            height: 1fr;
        }
        """

        def compose(self) -> ComposeResult:
            rich_log = RichLog(id="rich", min_width=4)
            rich_log.write(
                Text("deferred", style="on red", justify="right"),
                expand=True,
            )
            yield rich_log

    async with ExpandApp().run_test(size=(40, 8)) as pilot:
        rich = pilot.app.query_one(RichLog)
        await pilot.pause()

        def assert_right_justified(text: str, minimum: int) -> None:
            assert text.rstrip("\n").endswith("deferred") or text.rstrip("\n").endswith(
                "explicit"
            )
            body = text.rstrip("\n")
            assert body.strip() in {"deferred", "explicit"}
            assert len(body) >= minimum
            assert body.endswith(body.strip())

        first = _plain(rich, 0)
        content_width = rich.scrollable_content_region.width
        assert_right_justified(first, max(content_width, 4))
        assert rich.lines[0].cell_length >= max(content_width, 4)

        rich.write(Text("explicit", style="on green", justify="center"), expand=True)
        await pilot.pause()
        explicit = _plain(rich, 1).rstrip("\n")
        assert explicit.strip() == "explicit"
        assert explicit.startswith(" ") and explicit.endswith(" ")
        assert len(explicit) >= content_width

        await pilot.resize_terminal(60, 8)
        await pilot.pause()
        wider = rich.scrollable_content_region.width
        assert wider > content_width
        deferred = _plain(rich, 0).rstrip("\n")
        explicit = _plain(rich, 1).rstrip("\n")
        assert deferred.endswith("deferred")
        assert deferred.strip() == "deferred"
        assert len(deferred) >= wider
        assert explicit.strip() == "explicit"
        assert len(explicit) >= wider

        rich.min_width = 70
        await pilot.pause()
        deferred = _plain(rich, 0).rstrip("\n")
        assert deferred.endswith("deferred")
        assert len(deferred) >= 70
        assert rich.lines[0].cell_length >= 70


def test_follow_example_module_exposes_app() -> None:
    path = Path(__file__).resolve().parents[1] / "examples" / "rich_log_follow_state.py"
    spec = importlib.util.spec_from_file_location("rich_log_follow_state", path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    assert module.RichLogFollowStateApp.__name__ == "RichLogFollowStateApp"


async def test_follow_example_buttons() -> None:
    path = Path(__file__).resolve().parents[1] / "examples" / "rich_log_follow_state.py"
    spec = importlib.util.spec_from_file_location("rich_log_follow_state_app", path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    async with module.RichLogFollowStateApp().run_test(size=(100, 30)) as pilot:
        log = pilot.app.query_one("#log", Log)
        rich = pilot.app.query_one("#rich-log", RichLog)
        events = pilot.app.query_one("#events", RichLog)

        for _ in range(20):
            await pilot.click("#append-log")
            await pilot.click("#append-rich")
        await pilot.pause()
        log.scroll_home(animate=False, immediate=True)
        rich.scroll_home(animate=False, immediate=True)
        await pilot.pause()
        assert any(
            "FollowChanged" in _plain(events, index)
            for index in range(len(events.lines))
        )

        await pilot.click("#clear-events")
        await pilot.pause()
        assert events.lines == []

        await pilot.click("#follow-log")
        await pilot.click("#follow-rich")
        await pilot.pause()
        assert log.is_following_end is True
        assert rich.is_following_end is True
        assert log.is_vertical_scroll_end
        assert rich.is_vertical_scroll_end
        assert any(
            "FollowChanged" in _plain(events, index)
            for index in range(len(events.lines))
        )

        await pilot.click("#write-expanded")
        await pilot.pause()
        expanded = _plain(rich, -1).rstrip("\n")
        assert expanded.strip() == "expanded entry"
        assert expanded.endswith("expanded entry")
        assert len(expanded) >= rich.scrollable_content_region.width

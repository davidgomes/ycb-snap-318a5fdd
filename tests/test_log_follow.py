from __future__ import annotations

from rich.style import Style
from rich.text import Text

from examples.rich_log_follow_state import RichLogFollowStateApp
from textual.app import App, ComposeResult
from textual.strip import Strip
from textual.widgets import Log, RichLog


def _plain(strip: Strip) -> str:
    return "".join(segment.text for segment in strip._segments if segment.text)


def _styled_plain(strip: Strip) -> list[tuple[str, Style | None]]:
    return [
        (segment.text, segment.style) for segment in strip._segments if segment.text
    ]


class _LogApp(App):
    CSS = """
    Log { height: 4; }
    """

    def __init__(self, max_lines: int | None = None, auto_scroll: bool = True) -> None:
        super().__init__()
        self._max_lines = max_lines
        self._auto_scroll = auto_scroll
        self.follow_messages: list[Log.FollowChanged] = []

    def compose(self) -> ComposeResult:
        yield Log(max_lines=self._max_lines, auto_scroll=self._auto_scroll, id="log")

    def on_log_follow_changed(self, message: Log.FollowChanged) -> None:
        self.follow_messages.append(message)


class _RichApp(App):
    CSS = """
    RichLog { height: 4; }
    """

    def __init__(
        self,
        max_lines: int | None = None,
        auto_scroll: bool = True,
        min_width: int = 10,
    ) -> None:
        super().__init__()
        self._max_lines = max_lines
        self._auto_scroll = auto_scroll
        self._min_width = min_width
        self.follow_messages: list[RichLog.FollowChanged] = []

    def compose(self) -> ComposeResult:
        log = RichLog(
            max_lines=self._max_lines,
            auto_scroll=self._auto_scroll,
            min_width=self._min_width,
            id="rich",
        )
        yield log

    def on_rich_log_follow_changed(self, message: RichLog.FollowChanged) -> None:
        self.follow_messages.append(message)


def _expected_expand_width(log: RichLog) -> int:
    return max(log.scrollable_content_region.width, log.min_width)


async def test_log_follow_state_and_stable_viewport() -> None:
    app = _LogApp()
    async with app.run_test(size=(40, 16)) as pilot:
        log = pilot.app.query_one(Log)
        assert log.is_following_end is True
        for index in range(20):
            log.write_line(f"line {index}")
        await pilot.pause()
        assert log.is_following_end is True
        assert log.is_vertical_scroll_end
        assert log.vertical_scrollbar.position == log.scroll_y
        assert not app.follow_messages

        log.scroll_to(y=2, animate=False, immediate=True)
        await pilot.pause()
        assert log.is_following_end is False
        assert log.scroll_offset.y == 2
        assert log.vertical_scrollbar.position == log.scroll_y
        assert _plain(log.render_line(0)).startswith("line 2")
        assert len(app.follow_messages) == 1
        message = app.follow_messages[0]
        assert message.widget is log
        assert message.is_following_end is False
        assert message.scroll_y == log.scroll_y
        assert message.max_scroll_y == log.max_scroll_y

        log.scroll_to(y=1, animate=False, immediate=True)
        await pilot.pause()
        assert len(app.follow_messages) == 1
        assert log.vertical_scrollbar.position == log.scroll_y

        scroll_y = log.scroll_y
        log.write_line("extra")
        await pilot.pause()
        assert log.scroll_y == scroll_y
        assert log.is_following_end is False
        assert len(app.follow_messages) == 1

        log.scroll_end(animate=False, immediate=True)
        await pilot.pause()
        assert log.is_following_end is True
        assert log.is_vertical_scroll_end
        assert len(app.follow_messages) == 2
        assert app.follow_messages[1].is_following_end is True
        assert app.follow_messages[1].scroll_y == log.scroll_y
        assert app.follow_messages[1].max_scroll_y == log.max_scroll_y

        log.write_line("tail")
        await pilot.pause()
        assert log.is_vertical_scroll_end
        assert log.is_following_end is True
        assert len(app.follow_messages) == 2

        log.scroll_to(y=0, animate=False, immediate=True)
        await pilot.pause()
        log.follow_end()
        await pilot.pause()
        assert log.is_following_end is True
        assert log.is_vertical_scroll_end
        log.follow_end()
        await pilot.pause()
        assert len(app.follow_messages) == 4


async def test_log_write_and_max_lines_keep_viewport() -> None:
    app = _LogApp(max_lines=12)
    async with app.run_test(size=(40, 16)) as pilot:
        log = pilot.app.query_one(Log)
        for index in range(12):
            log.write_line(f"row {index}")
        await pilot.pause()
        log.scroll_to(y=4, animate=False, immediate=True)
        await pilot.pause()
        assert log.is_following_end is False
        assert _plain(log.render_line(0)).startswith("row 4")
        log.write_line("row 12")
        log.write_line("row 13")
        await pilot.pause()
        # Two lines were appended and two were pruned from the top.
        assert log.scroll_y == 2
        assert log.is_following_end is False
        assert _plain(log.render_line(0)).startswith("row 4")
        assert log.vertical_scrollbar.position == log.scroll_y


async def test_log_auto_scroll_disabled_stops_following() -> None:
    app = _LogApp(auto_scroll=False)
    async with app.run_test(size=(30, 16)) as pilot:
        log = pilot.app.query_one(Log)
        for index in range(20):
            log.write_line(f"line {index}")
        await pilot.pause()
        assert log.scroll_y == 0
        assert log.is_following_end is False
        assert log.vertical_scrollbar.position == 0


async def test_rich_log_follow_and_prune_viewport() -> None:
    app = _RichApp(max_lines=12, min_width=4)
    async with app.run_test(size=(40, 16)) as pilot:
        log = pilot.app.query_one(RichLog)
        for index in range(12):
            log.write(f"row {index}")
        await pilot.pause()
        assert log.is_following_end is True
        assert log.is_vertical_scroll_end
        assert log.vertical_scrollbar.position == log.scroll_y

        log.scroll_to(y=3, animate=False, immediate=True)
        await pilot.pause()
        assert log.is_following_end is False
        assert len(app.follow_messages) == 1
        assert app.follow_messages[0].widget is log
        assert app.follow_messages[0].is_following_end is False
        assert app.follow_messages[0].scroll_y == log.scroll_y
        assert app.follow_messages[0].max_scroll_y == log.max_scroll_y
        assert _plain(log.render_line(0)).startswith("row 3")

        log.scroll_to(y=2, animate=False, immediate=True)
        await pilot.pause()
        assert len(app.follow_messages) == 1
        log.scroll_to(y=3, animate=False, immediate=True)
        await pilot.pause()

        log.write("row 12")
        log.write("row 13")
        await pilot.pause()
        assert log.scroll_y == 1
        assert log.is_following_end is False
        assert _plain(log.render_line(0)).startswith("row 3")
        assert log.vertical_scrollbar.position == log.scroll_y

        log.scroll_to(y=log.max_scroll_y, animate=False, immediate=True)
        await pilot.pause()
        assert log.is_following_end is True
        log.write("row 12")
        await pilot.pause()
        assert log.is_vertical_scroll_end
        assert (
            len(
                [message for message in app.follow_messages if message.is_following_end]
            )
            == 1
        )


def _assert_justified(log: RichLog, text: str, *, justify: str) -> None:
    width = _expected_expand_width(log)
    matched = [_plain(line) for line in log.lines if text in _plain(line)]
    assert matched, f"{text!r} missing from {[_plain(line) for line in log.lines]!r}"
    plain = matched[-1]
    assert len(plain) == width
    if justify == "right":
        assert plain.endswith(text)
        assert plain[: -len(text)] == " " * (width - len(text))
    elif justify == "center":
        assert plain.strip() == text
        assert plain.startswith(" ")
        assert plain.endswith(" ")


async def test_rich_log_expand_justify_deferred_explicit_and_resize() -> None:
    class ExpandApp(App):
        def compose(self) -> ComposeResult:
            rich_log = RichLog(min_width=8, id="rich")
            rich_log.write(
                Text("deferred", style="on red", justify="right"),
                expand=True,
            )
            yield rich_log

        def on_mount(self) -> None:
            self.query_one(RichLog).write(
                Text("explicit", style="on blue", justify="center"),
                expand=True,
            )

    async with ExpandApp().run_test(size=(30, 10)) as pilot:
        log = pilot.app.query_one(RichLog)
        await pilot.pause()
        _assert_justified(log, "deferred", justify="right")
        _assert_justified(log, "explicit", justify="center")
        deferred = next(line for line in log.lines if "deferred" in _plain(line))
        assert any(
            style is not None and style.bgcolor is not None
            for _, style in _styled_plain(deferred)
        )

        await pilot.resize_terminal(48, 12)
        _assert_justified(log, "deferred", justify="right")
        _assert_justified(log, "explicit", justify="center")
        assert _expected_expand_width(log) > 8

        log.min_width = 60
        await pilot.pause()
        _assert_justified(log, "deferred", justify="right")
        _assert_justified(log, "explicit", justify="center")
        assert _expected_expand_width(log) >= 60


async def test_follow_state_example_buttons() -> None:
    app = RichLogFollowStateApp()
    async with app.run_test(size=(80, 24)) as pilot:
        log = pilot.app.query_one("#log", Log)
        rich = pilot.app.query_one("#rich-log", RichLog)
        events = pilot.app.query_one("#events", RichLog)
        for _ in range(12):
            await pilot.click("#append-log")
            await pilot.click("#append-rich")
        assert log.is_following_end is True
        assert rich.is_following_end is True

        log.scroll_to(y=1, animate=False, immediate=True)
        rich.scroll_to(y=1, animate=False, immediate=True)
        await pilot.pause()
        assert log.is_following_end is False
        assert rich.is_following_end is False
        assert any("FollowChanged" in _plain(line) for line in events.lines)

        await pilot.click("#follow-log")
        await pilot.click("#follow-rich")
        await pilot.pause()
        assert log.is_following_end is True
        assert log.is_vertical_scroll_end
        assert rich.is_following_end is True
        assert rich.is_vertical_scroll_end
        assert log.vertical_scrollbar.position == log.scroll_y
        assert rich.vertical_scrollbar.position == rich.scroll_y

        await pilot.click("#write-expanded")
        await pilot.pause()
        expanded = [line for line in rich.lines if "expanded" in _plain(line)]
        assert expanded
        width = max(rich.scrollable_content_region.width, rich.min_width)
        assert len(_plain(expanded[-1])) == width
        assert _plain(expanded[-1]).endswith("expanded")

        await pilot.click("#clear-events")
        await pilot.pause()
        assert events.lines == []


async def test_rich_log_long_lines_are_not_cropped() -> None:
    class LongApp(App):
        def compose(self) -> ComposeResult:
            yield RichLog(min_width=4, id="rich")

    async with LongApp().run_test(size=(20, 8)) as pilot:
        log = pilot.app.query_one(RichLog)
        payload = "x" * 50
        log.write(payload)
        await pilot.pause()
        assert _plain(log.lines[0]) == payload

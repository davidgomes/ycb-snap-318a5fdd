from __future__ import annotations

from textual.app import App, ComposeResult
from textual.widgets import Log


async def test_process_line():
    log = Log()
    assert log._process_line("foo") == "foo"
    assert log._process_line("foo\t") == "foo     "
    assert log._process_line("\0foo") == "�foo"


async def test_disabled_log_no_attribute_error() -> None:
    """Ensure that initializing the log with disabled=True does not
    raise an AttributeError.
    Regression test for https://github.com/Textualize/textual/issues/5028
    """

    class DisabledLogApp(App):
        def compose(self) -> ComposeResult:
            yield Log(disabled=True)

    async with DisabledLogApp().run_test() as pilot:
        # If no exception is raised, the test will pass
        log = pilot.app.query_one(Log)
        assert log.disabled == True


class FollowLogApp(App):
    CSS = "Log { height: 10; }"

    def __init__(self, max_lines: int | None = None) -> None:
        super().__init__()
        self.max_lines = max_lines
        self.messages: list[Log.FollowChanged] = []

    def compose(self) -> ComposeResult:
        yield Log(max_lines=self.max_lines)

    def on_mount(self) -> None:
        self.query_one(Log).write_lines([f"line {n}" for n in range(50)])

    def on_log_follow_changed(self, message: Log.FollowChanged) -> None:
        self.messages.append(message)


async def test_log_follow_state() -> None:
    app = FollowLogApp()
    async with app.run_test() as pilot:
        log = app.query_one(Log)
        await pilot.pause()
        assert log.is_following_end
        assert log.scroll_y == log.max_scroll_y > 0
        max_scroll_y = log.max_scroll_y

        log.scroll_to(y=5, animate=False)
        await pilot.pause()
        assert not log.is_following_end
        assert log.scroll_offset.y == 5
        assert log.vertical_scrollbar.position == 5

        log.scroll_to(y=3, animate=False)
        log.write_line("new line")
        log.write("partial")
        await pilot.pause()
        assert log.scroll_offset.y == 3
        assert not log.is_following_end

        log.scroll_to(y=log.max_scroll_y, animate=False)
        await pilot.pause()
        assert log.is_following_end
        log.write_line("another line")
        await pilot.pause()
        assert log.scroll_y == log.max_scroll_y

        log.scroll_to(y=0, animate=False)
        await pilot.pause()
        log.follow_end()
        await pilot.pause()
        assert log.is_following_end
        assert log.scroll_y == log.max_scroll_y

        assert [message.is_following_end for message in app.messages] == [
            False,
            True,
            False,
            True,
        ]
        first = app.messages[0]
        assert first.widget is log
        assert first.control is log
        assert first.scroll_y == 5
        assert first.max_scroll_y == max_scroll_y


async def test_log_prune_keeps_viewport() -> None:
    app = FollowLogApp(max_lines=50)
    async with app.run_test() as pilot:
        log = app.query_one(Log)
        await pilot.pause()
        log.scroll_to(y=20, animate=False)
        await pilot.pause()
        assert log.lines[log.scroll_offset.y] == "line 20"

        log.write_lines(["a", "b", "c"])
        await pilot.pause()
        assert log.scroll_offset.y == 17
        assert log.lines[log.scroll_offset.y] == "line 20"
        assert not log.is_following_end

        log.write("d\n")
        await pilot.pause()
        assert log.lines[log.scroll_offset.y] == "line 20"

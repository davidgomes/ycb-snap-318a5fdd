from textual.app import App, ComposeResult
from textual.widgets import Log


class LogFollowApp(App):
    def compose(self) -> ComposeResult:
        yield Log(id="log", max_lines=8)

    def on_mount(self) -> None:
        log = self.query_one(Log)
        for index in range(20):
            log.write_line(f"line {index}")


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


async def test_log_follow_releases_and_restores() -> None:
    """Scrolling away from the end stops following until the end is visible again."""
    events: list[tuple[bool, float, int]] = []

    class AppWithEvents(LogFollowApp):
        def on_log_follow_changed(self, event: Log.FollowChanged) -> None:
            events.append((event.is_following_end, event.scroll_y, event.max_scroll_y))

    async with AppWithEvents().run_test(size=(80, 6)) as pilot:
        log = pilot.app.query_one(Log)
        await pilot.pause()
        assert log.is_following_end is True
        assert log.max_scroll_y > 0
        assert log.scroll_y == log.max_scroll_y
        assert events == []

        log.scroll_to(y=1, animate=False, immediate=True)
        await pilot.pause()
        assert log.scroll_y == 1
        assert log.is_following_end is False
        assert events[-1][0] is False
        held = log.render_line(0).text

        log.write_line("extra")
        await pilot.pause()
        # max_lines drops one line from the top, so the viewport stays put.
        assert log.is_following_end is False
        assert log.scroll_y == 0
        assert log.render_line(0).text == held
        assert len(events) == 1

        log.scroll_end(animate=False, immediate=True, x_axis=False)
        await pilot.pause()
        assert log.is_following_end is True
        assert events[-1][0] is True
        assert log.scroll_y == log.max_scroll_y

        log.scroll_to(y=0, animate=False, immediate=True)
        await pilot.pause()
        log.follow_end()
        await pilot.pause()
        assert log.is_following_end is True
        assert log.scroll_y == log.max_scroll_y

from rich.text import Text

from textual.app import App, ComposeResult
from textual.widgets import RichLog


async def test_make_renderable_expand_tabs():
    # Regression test for https://github.com/Textualize/textual/issues/3007
    text_log = RichLog()
    renderable = text_log._make_renderable("\tfoo")
    assert isinstance(renderable, Text)
    assert renderable.plain == "        foo"


class RichLogJustifyApp(App):
    def compose(self) -> ComposeResult:
        rich_log = RichLog(id="rich", min_width=10, max_lines=6)
        # Deferred until the width is known, then expanded and justified.
        rich_log.write(Text("0123456789", style="on red", justify="right"), expand=True)
        yield rich_log


def _line_text(rich_log: RichLog, y: int = 0) -> str:
    return rich_log.render_line(y).text.rstrip("\n")


async def test_rich_log_expand_justify_and_reflow() -> None:
    """Expanded writes stay right-justified across deferral, resize, and min_width."""
    async with RichLogJustifyApp().run_test(size=(30, 6)) as pilot:
        rich_log = pilot.app.query_one(RichLog)
        await pilot.pause()
        line = _line_text(rich_log)
        assert line.endswith("0123456789")
        assert line.index("0") > 0
        width_before = rich_log.lines[0].cell_length

        await pilot.resize_terminal(50, 6)
        widened = _line_text(rich_log)
        assert widened.endswith("0123456789")
        assert rich_log.lines[0].cell_length > width_before

        rich_log.min_width = 60
        await pilot.pause()
        stored = rich_log.lines[0].text
        assert stored.rstrip().endswith("0123456789")
        assert rich_log.lines[0].cell_length >= 60
        # The viewport is narrower than min_width, so the right edge is clipped.
        assert not _line_text(rich_log).rstrip().endswith("0123456789")


async def test_rich_log_does_not_snap_back_when_scrolled_up() -> None:
    events: list[bool] = []

    class FollowApp(App):
        def compose(self) -> ComposeResult:
            yield RichLog(id="rich", max_lines=8)

        def on_mount(self) -> None:
            rich_log = self.query_one(RichLog)
            for index in range(20):
                rich_log.write(f"line {index}")

        def on_rich_log_follow_changed(self, event: RichLog.FollowChanged) -> None:
            events.append(event.is_following_end)

    async with FollowApp().run_test(size=(80, 6)) as pilot:
        rich_log = pilot.app.query_one(RichLog)
        await pilot.pause()
        assert rich_log.is_following_end is True
        assert rich_log.max_scroll_y > 0
        assert rich_log.scroll_y == rich_log.max_scroll_y
        assert events == []

        rich_log.scroll_to(y=1, animate=False, immediate=True)
        await pilot.pause()
        assert rich_log.scroll_y == 1
        assert rich_log.is_following_end is False
        assert events == [False]
        held = _line_text(rich_log)

        rich_log.write("extra")
        await pilot.pause()
        assert rich_log.is_following_end is False
        assert events == [False]
        assert rich_log.scroll_y == 0
        assert _line_text(rich_log) == held
        assert rich_log.lines[-1].text.strip() == "extra"

        rich_log.scroll_end(animate=False, immediate=True, x_axis=False)
        await pilot.pause()
        assert rich_log.is_following_end is True
        assert events == [False, True]
        rich_log.write("tail")
        await pilot.pause()
        assert rich_log.is_following_end is True
        assert rich_log.scroll_y == rich_log.max_scroll_y
        assert rich_log.lines[-1].text.strip() == "tail"

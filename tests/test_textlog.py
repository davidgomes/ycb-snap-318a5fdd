from rich.text import Text

from textual.app import App, ComposeResult
from textual.widgets import RichLog


async def test_make_renderable_expand_tabs():
    # Regression test for https://github.com/Textualize/textual/issues/3007
    text_log = RichLog()
    renderable = text_log._make_renderable("\tfoo")
    assert isinstance(renderable, Text)
    assert renderable.plain == "        foo"


class ExpandApp(App[None]):
    CSS = "RichLog { height: 10; }"

    def compose(self) -> ComposeResult:
        rich_log = RichLog(min_width=10)
        rich_log.write(Text("deferred", justify="right"), expand=True)
        yield rich_log


async def test_write_expand_justifies_to_full_width():
    app = ExpandApp()
    async with app.run_test(size=(40, 12)) as pilot:
        rich_log = app.query_one(RichLog)
        rich_log.write(Text("explicit", justify="center"), expand=True)
        rich_log.write(Text("plain", justify="right"))
        await pilot.pause()
        width = rich_log.scrollable_content_region.width
        assert [line.text for line in rich_log.lines] == [
            "deferred".rjust(width),
            "explicit".center(width),
            "plain".rjust(10),
        ]


async def test_expanded_entries_rerender_on_resize_and_min_width():
    app = ExpandApp()
    async with app.run_test(size=(40, 12)) as pilot:
        rich_log = app.query_one(RichLog)
        rich_log.write(Text("plain", justify="right"))
        await pilot.pause()

        await pilot.resize_terminal(30, 12)
        await pilot.pause()
        width = rich_log.scrollable_content_region.width
        assert [line.text for line in rich_log.lines] == [
            "deferred".rjust(width),
            "plain".rjust(10),
        ]
        assert rich_log.virtual_size.width == width

        rich_log.min_width = width + 6
        await pilot.pause()
        assert [line.text for line in rich_log.lines] == [
            "deferred".rjust(width + 6),
            "plain".rjust(10),
        ]


async def test_rerender_keeps_viewport_when_not_following():
    class WrapApp(App[None]):
        CSS = "RichLog { height: 10; }"

        def compose(self) -> ComposeResult:
            yield RichLog(wrap=True, min_width=1)

    app = WrapApp()
    async with app.run_test(size=(40, 12)) as pilot:
        rich_log = app.query_one(RichLog)
        for n in range(30):
            rich_log.write(Text(f"entry {n:02}" + " word" * 7), expand=True)
        await pilot.pause()
        assert len(rich_log.lines) == 60

        rich_log.scroll_to(y=20, animate=False)
        await pilot.pause()
        assert rich_log.render_line(0).text.startswith("entry 10")

        await pilot.resize_terminal(80, 12)
        await pilot.pause()
        assert len(rich_log.lines) == 30
        assert not rich_log.is_following_end
        assert rich_log.scroll_y == 10
        assert rich_log.render_line(0).text.startswith("entry 10")

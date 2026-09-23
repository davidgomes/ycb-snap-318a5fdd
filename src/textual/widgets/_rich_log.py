"""Provides a scrollable text-logging widget."""

from __future__ import annotations

from collections import deque
from typing import TYPE_CHECKING, NamedTuple, Optional, cast

from rich.console import Console, RenderableType
from rich.highlighter import Highlighter, ReprHighlighter
from rich.measure import measure_renderables
from rich.pretty import Pretty
from rich.protocol import is_renderable
from rich.segment import Segment
from rich.text import Text

from textual.cache import LRUCache
from textual.events import Resize
from textual.geometry import Size
from textual.message import Message
from textual.reactive import var
from textual.strip import Strip
from textual.widgets._end_follow import _EndFollow

if TYPE_CHECKING:
    from typing_extensions import Self


class DeferredRender(NamedTuple):
    """A renderable which is awaiting rendering.
    This may happen if a `write` occurs before the width is known.

    The arguments are the same as for `RichLog.write`, as this just
    represents a deferred call to that method.
    """

    content: RenderableType | object
    """The content to render."""
    width: int | None = None
    """The width to render or `None` to use optimal width."""
    expand: bool = False
    """Enable expand to widget width, or `False` to use `width`."""
    shrink: bool = True
    """Enable shrinking of content to fit width."""
    scroll_end: bool | None = None
    """Enable automatic scroll to end, or `None` to use `self.auto_scroll`."""


class _RichLogEntry:
    """One `write` stored so expanded lines can be rendered again."""

    __slots__ = ("content", "width", "expand", "shrink", "lines", "cropped")

    def __init__(
        self,
        content: RenderableType | object,
        width: int | None,
        expand: bool,
        shrink: bool,
        lines: list[Strip],
    ) -> None:
        self.content = content
        self.width = width
        self.expand = expand
        self.shrink = shrink
        self.lines = lines
        self.cropped = 0


class RichLog(_EndFollow, can_focus=True):
    """A widget for logging Rich renderables and text."""

    DEFAULT_CSS = """
    RichLog{
        background: $surface;
        color: $foreground;
        overflow-y: scroll;
        &:focus {
            background-tint: $foreground 5%;
        }
    }
    """

    max_lines: var[int | None] = var[Optional[int]](None)
    min_width: var[int] = var(78)
    wrap: var[bool] = var(False)
    highlight: var[bool] = var(False)
    markup: var[bool] = var(False)
    auto_scroll: var[bool] = var(True)
    """Automatically scroll to the end on write while following the end."""

    class FollowChanged(Message):
        """Posted when [is_following_end][textual.widgets.RichLog.is_following_end] changes.

        This message is not posted when the flag is set to the value it already
        holds. Handle it with ``on_rich_log_follow_changed``.
        """

        def __init__(
            self,
            widget: RichLog,
            is_following_end: bool,
            scroll_y: float,
            max_scroll_y: int,
        ) -> None:
            super().__init__()
            self.widget: RichLog = widget
            """The log whose follow state changed."""
            self.is_following_end: bool = is_following_end
            """The new follow-end state."""
            self.scroll_y: float = scroll_y
            """Vertical scroll position when the state changed."""
            self.max_scroll_y: int = max_scroll_y
            """Maximum vertical scroll position when the state changed."""

        @property
        def control(self) -> RichLog:
            """The log associated with this message."""
            return self.widget

    def __init__(
        self,
        *,
        max_lines: int | None = None,
        min_width: int = 78,
        wrap: bool = False,
        highlight: bool = False,
        markup: bool = False,
        auto_scroll: bool = True,
        name: str | None = None,
        id: str | None = None,
        classes: str | None = None,
        disabled: bool = False,
    ) -> None:
        """Create a `RichLog` widget.

        Args:
            max_lines: Maximum number of lines in the log or `None` for no maximum.
            min_width: Width to use for calls to `write` with no specified `width`.
            wrap: Enable word wrapping (default is off).
            highlight: Automatically highlight content. By default, the `ReprHighlighter` is used.
                To customize highlighting, set `highlight=True` and then set the `highlighter`
                attribute to an instance of `Highlighter`.
            markup: Apply Rich console markup.
            auto_scroll: Enable automatic scrolling to end.
            name: The name of the text log.
            id: The ID of the text log in the DOM.
            classes: The CSS classes of the text log.
            disabled: Whether the text log is disabled or not.
        """
        self._following_end = True
        self._suppress_follow_sync = 0
        self._hold_follow = False
        super().__init__(name=name, id=id, classes=classes, disabled=disabled)
        self._start_line: int = 0
        self.lines: list[Strip] = []
        """The lines currently visible in the log."""
        self._entries: list[_RichLogEntry] = []
        self._line_cache: LRUCache[tuple[int, int, int, int], Strip]
        self._line_cache = LRUCache(1024)
        self._deferred_renders: deque[DeferredRender] = deque()
        """Queue of deferred renderables to be rendered."""
        self._expand_key: tuple[int, int] | None = None
        self._rerendering = False
        self.max_lines = max_lines
        """Maximum number of lines in the log or `None` for no maximum."""
        self.wrap = wrap
        """Enable word wrapping."""
        self.highlight = highlight
        """Automatically highlight content."""
        self.markup = markup
        """Apply Rich console markup."""
        self.auto_scroll = auto_scroll
        """Automatically scroll to the end on write."""
        self.highlighter: Highlighter = ReprHighlighter()
        """Rich Highlighter used to highlight content when highlight is True"""

        self._widest_line_width = 0
        """The width of the widest line currently in the log."""

        self._size_known = False
        """Flag which is set to True when the size of the RichLog is known,
        indicating we can proceed with rendering deferred writes."""
        # Set last so the watcher can see the structures initialized above.
        self.min_width = min_width
        """Minimum width of renderables."""

    def notify_style_update(self) -> None:
        super().notify_style_update()
        self._line_cache.clear()

    def on_resize(self, event: Resize) -> None:
        if event.size.width and not self._size_known:
            # This size is known for the first time.
            self._size_known = True
            deferred_renders = self._deferred_renders
            while deferred_renders:
                deferred_render = deferred_renders.popleft()
                self.write(*deferred_render)
        if self._size_known:
            self._rerender_expanded()

    def watch_min_width(self) -> None:
        """Re-render expanded entries when the minimum width changes."""
        self._rerender_expanded()

    def get_content_width(self, container: Size, viewport: Size) -> int:
        if self._size_known:
            return self.virtual_size.width
        else:
            return container.width

    def _make_renderable(self, content: RenderableType | object) -> RenderableType:
        """Make content renderable.

        Args:
            content: Content to render.

        Returns:
            A Rich renderable.
        """
        renderable: RenderableType
        if not is_renderable(content):
            renderable = Pretty(content)
        else:
            if isinstance(content, str):
                if self.markup:
                    renderable = Text.from_markup(content)
                else:
                    renderable = Text(content)
                if self.highlight:
                    renderable = self.highlighter(renderable)
            else:
                renderable = cast(RenderableType, content)

        if isinstance(renderable, Text):
            renderable.expand_tabs()

        return renderable

    def _expand_layout_key(self) -> tuple[int, int]:
        return (self.scrollable_content_region.width, self.min_width)

    def _resolve_render_width(
        self,
        renderable: RenderableType,
        width: int | None,
        expand: bool,
        shrink: bool,
        console: Console,
        render_options,
    ) -> int:
        """Width used to render one write."""
        if width is not None:
            # Use the width specified by the caller.
            # We ignore `expand` and `shrink` when a width is specified.
            # This also overrides `min_width` set on the RichLog.
            return width

        renderable_width = measure_renderables(
            console, render_options, [renderable]
        ).maximum
        render_width = renderable_width
        scrollable_content_width = self.scrollable_content_region.width

        if expand and renderable_width < scrollable_content_width:
            # Expand the renderable to the width of the scrollable content region.
            render_width = max(renderable_width, scrollable_content_width)

        if shrink and renderable_width > scrollable_content_width:
            # Shrink the renderable down to fit within the scrollable content region.
            render_width = min(renderable_width, scrollable_content_width)

        # The user has not supplied a width, so make sure min_width is respected.
        return max(render_width, self.min_width)

    def _render_justified_text(self, text: Text, render_width: int) -> list[Strip]:
        """Render text so justification pads to ``render_width``.

        Current Rich skips justification when ``overflow="ignore"``. Short lines
        are rendered with ``overflow="crop"`` so padding is applied. Longer lines
        stay intact so horizontal scrolling still works.
        """
        console = self.app.console
        rendered_lines: list[list[Segment]] = []
        justify = text.justify
        for line in text.split(allow_blank=True):
            if justify and line.cell_len < render_width:
                options = console.options.update_width(render_width).update(
                    overflow="crop", no_wrap=True
                )
            else:
                options = console.options.update_width(max(render_width, 1)).update(
                    overflow="ignore", no_wrap=True
                )
            if line.justify is None and justify is not None:
                line = line.copy()
                line.justify = justify
            segments = list(console.render(line, options))
            split_lines = list(Segment.split_lines(segments))
            if split_lines:
                rendered_lines.extend(split_lines)
            else:
                rendered_lines.append([])
        if not rendered_lines:
            return [Strip.blank(render_width)]
        return Strip.from_lines(rendered_lines)

    def _render_strips(
        self,
        content: RenderableType | object,
        width: int | None,
        expand: bool,
        shrink: bool,
    ) -> list[Strip]:
        """Render one write to strips."""
        if isinstance(content, Text):
            content = content.copy()
        renderable = self._make_renderable(content)

        console = self.app.console
        render_options = console.options

        if isinstance(renderable, Text) and not self.wrap:
            render_options = render_options.update(overflow="ignore", no_wrap=True)

        render_width = self._resolve_render_width(
            renderable, width, expand, shrink, console, render_options
        )

        if (
            isinstance(renderable, Text)
            and not self.wrap
            and renderable.justify
            and render_width > 0
        ):
            return self._render_justified_text(renderable, render_width)

        render_options = render_options.update_width(render_width)
        segments = self.app.console.render(renderable, render_options)
        lines = list(Segment.split_lines(segments))

        if not lines:
            self._widest_line_width = max(render_width, self._widest_line_width)
            return [Strip.blank(render_width)]

        strips = Strip.from_lines(lines)
        for strip in strips:
            strip.adjust_cell_length(render_width)
        return strips

    def _rebuild_lines(self) -> None:
        lines: list[Strip] = []
        for entry in self._entries:
            lines.extend(entry.lines)
        self.lines = lines

    def _recompute_widest(self) -> None:
        self._widest_line_width = max(
            (line.cell_length for line in self.lines),
            default=0,
        )

    def _note_strip_widths(self, strips: list[Strip]) -> None:
        if strips:
            self._widest_line_width = max(
                self._widest_line_width,
                max(strip.cell_length for strip in strips),
            )

    def _trim_lines(self, count: int) -> None:
        """Drop ``count`` lines from the start of the log."""
        if count <= 0:
            return
        self._start_line += count
        remaining = count
        while remaining > 0 and self._entries:
            entry = self._entries[0]
            have = len(entry.lines)
            if have <= remaining:
                remaining -= have
                self._entries.pop(0)
            else:
                del entry.lines[:remaining]
                entry.cropped += remaining
                remaining = 0
        self._rebuild_lines()
        self._recompute_widest()

    def _rerender_expanded(self) -> None:
        """Render expanded writes again after a resize or ``min_width`` change."""
        if not self._size_known or self._rerendering:
            return
        key = self._expand_layout_key()
        if key == self._expand_key:
            return
        expanded = [
            entry for entry in self._entries if entry.expand and entry.width is None
        ]
        self._expand_key = key
        if not expanded:
            return

        was_following = self.is_following_end
        previous_scroll_y = self.scroll_y
        self._rerendering = True
        self._suppress_follow_sync += 1
        try:
            for entry in expanded:
                lines = self._render_strips(entry.content, None, True, entry.shrink)
                if entry.cropped:
                    lines = lines[entry.cropped :]
                entry.lines = lines
            self._rebuild_lines()
            self._line_cache.clear()
            self._recompute_widest()
            if self.max_lines is not None and len(self.lines) > self.max_lines:
                self._trim_lines(len(self.lines) - self.max_lines)
            self.virtual_size = Size(self._widest_line_width, len(self.lines))
            self.refresh()
        finally:
            self._suppress_follow_sync -= 1
            self._rerendering = False

        if was_following and self.auto_scroll:
            self._scroll_end_and_follow(animate=False, immediate=True)
        else:
            self._suppress_follow_sync += 1
            try:
                self.scroll_to(y=previous_scroll_y, animate=False, immediate=True)
            finally:
                self._suppress_follow_sync -= 1
            self._set_following_end(self.is_vertical_scroll_end)

    def write(
        self,
        content: RenderableType | object,
        width: int | None = None,
        expand: bool = False,
        shrink: bool = True,
        scroll_end: bool | None = None,
        animate: bool = False,
    ) -> Self:
        """Write a string or a Rich renderable to the bottom of the log.

        Notes:
            The rendering of content will be deferred until the size of the `RichLog` is known.
            This means if you call `write` in `compose` or `on_mount`, the content will not be
            rendered immediately.

            Writes made with ``expand=True`` are rendered again when the widget is
            resized or ``min_width`` changes, so justification tracks the new width.

        Args:
            content: Rich renderable (or a string).
            width: Width to render, or `None` to use `RichLog.min_width`.
                If specified, `expand` and `shrink` will be ignored.
            expand: Permit expanding of content to the width of the content region of the RichLog.
                If `width` is specified, then `expand` will be ignored.
            shrink: Permit shrinking of content to fit within the content region of the RichLog.
                If `width` is specified, then `shrink` will be ignored.
            scroll_end: Enable automatic scroll to end, or `None` to use `self.auto_scroll`.
            animate: Enable animation if the log will scroll.

        Returns:
            The `RichLog` instance.
        """
        if isinstance(content, Text):
            content = content.copy()

        if not self._size_known:
            # We don't know the size yet, so we'll need to render this later.
            # We defer ALL writes until the size is known, to ensure ordering is preserved.
            self._deferred_renders.append(
                DeferredRender(content, width, expand, shrink, scroll_end)
            )
            return self

        self._rerender_expanded()

        was_following = self.is_following_end
        previous_scroll_y = self.scroll_y
        strips = self._render_strips(content, width, expand, shrink)
        removed_lines = 0
        self._suppress_follow_sync += 1
        try:
            entry = _RichLogEntry(content, width, expand, shrink, strips)
            self._entries.append(entry)
            self.lines.extend(strips)
            self._note_strip_widths(strips)
            if self.max_lines is not None and len(self.lines) > self.max_lines:
                removed_lines = len(self.lines) - self.max_lines
                self._trim_lines(removed_lines)
            self._expand_key = self._expand_layout_key()
            self.virtual_size = Size(self._widest_line_width, len(self.lines))
        finally:
            self._suppress_follow_sync -= 1

        self._follow_after_write(
            was_following=was_following,
            scroll_end=scroll_end,
            previous_scroll_y=previous_scroll_y,
            removed_lines=removed_lines,
            animate=animate,
            immediate=False,
        )
        self.refresh()
        return self

    def clear(self) -> Self:
        """Clear the text log.

        Returns:
            The `RichLog` instance.
        """
        self._suppress_follow_sync += 1
        try:
            self.lines.clear()
            self._entries.clear()
            self._line_cache.clear()
            self._start_line = 0
            self._widest_line_width = 0
            self._deferred_renders.clear()
            self.virtual_size = Size(0, len(self.lines))
            self.scroll_to(y=0, animate=False, immediate=True)
            self.refresh()
        finally:
            self._suppress_follow_sync -= 1
        self._set_following_end(True)
        return self

    def render_line(self, y: int) -> Strip:
        scroll_x, scroll_y = self.scroll_offset
        line = self._render_line(
            scroll_y + y, scroll_x, self.scrollable_content_region.width
        )
        strip = line.apply_style(self.rich_style)
        return strip

    def _render_line(self, y: int, scroll_x: int, width: int) -> Strip:
        if y >= len(self.lines):
            return Strip.blank(width, self.rich_style)

        key = (y + self._start_line, scroll_x, width, self._widest_line_width)
        if key in self._line_cache:
            return self._line_cache[key]

        line = self.lines[y].crop_extend(scroll_x, scroll_x + width, self.rich_style)

        self._line_cache[key] = line
        return line

"""Provides a scrollable text-logging widget."""

from __future__ import annotations

from collections import deque
from typing import TYPE_CHECKING, NamedTuple, Optional, cast

from rich.console import ConsoleOptions, JustifyMethod, RenderableType
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
from textual.scroll_view import ScrollView
from textual.strip import Strip

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


class _LogEntry:
    """The lines produced by a single call to `RichLog.write`."""

    __slots__ = ["line_count", "widest", "render_width", "renderable", "shrink"]

    def __init__(
        self,
        line_count: int,
        widest: int,
        render_width: int,
        renderable: RenderableType | None,
        shrink: bool,
    ) -> None:
        self.line_count = line_count
        """Number of lines of this entry still in the log."""
        self.widest = widest
        """Width of the widest rendered line."""
        self.render_width = render_width
        """The width the entry was rendered at."""
        self.renderable = renderable
        """The renderable, if the entry expands and must be re-rendered when the width changes."""
        self.shrink = shrink
        """Enable shrinking of content to fit width."""


class RichLog(ScrollView, can_focus=True):
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

    class FollowChanged(Message):
        """Posted when the log starts or stops following the end of its content."""

        def __init__(
            self,
            widget: RichLog,
            is_following_end: bool,
            scroll_y: float,
            max_scroll_y: int,
        ) -> None:
            super().__init__()
            self.widget: RichLog = widget
            """The `RichLog` whose follow state changed."""
            self.is_following_end: bool = is_following_end
            """`True` if the log is now following the end, otherwise `False`."""
            self.scroll_y: float = scroll_y
            """The vertical scroll offset at the time of the change."""
            self.max_scroll_y: int = max_scroll_y
            """The maximum vertical scroll offset at the time of the change."""

        @property
        def control(self) -> RichLog:
            """Alias for `widget`."""
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
        super().__init__(name=name, id=id, classes=classes, disabled=disabled)
        self._size_known = False
        """Flag which is set to True when the size of the RichLog is known,
        indicating we can proceed with rendering deferred writes."""
        self._following_end = True
        """Is the log following (pinned to) the end of its content?"""
        self.max_lines = max_lines
        """Maximum number of lines in the log or `None` for no maximum."""
        self._start_line: int = 0
        self.lines: list[Strip] = []
        """The lines currently visible in the log."""
        self._line_cache: LRUCache[tuple[int, int, int, int], Strip]
        self._line_cache = LRUCache(1024)
        self._deferred_renders: deque[DeferredRender] = deque()
        """Queue of deferred renderables to be rendered."""
        self._entries: deque[_LogEntry] = deque()
        """One entry per write, in the same order as `lines`."""
        self.min_width = min_width
        """Minimum width of renderables."""
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

    @property
    def is_following_end(self) -> bool:
        """Is the log following the end of its content?

        While following, writes will scroll to the end (if `auto_scroll` is enabled).
        Scrolling away from the end stops following; scrolling back to the end resumes it.
        """
        return self._following_end

    def _set_following_end(self, following_end: bool) -> None:
        """Update the follow state, posting `FollowChanged` if it changed.

        Args:
            following_end: New follow state.
        """
        if following_end != self._following_end:
            self._following_end = following_end
            self.post_message(
                self.FollowChanged(
                    self, following_end, self.scroll_y, self.max_scroll_y
                )
            )

    def follow_end(self, animate: bool = False) -> None:
        """Scroll to the end and follow new content.

        Args:
            animate: Animate the scroll to the end.
        """
        self._set_following_end(True)
        self.scroll_end(animate=animate, immediate=True, x_axis=False)

    def watch_scroll_y(self, old_value: float, new_value: float) -> None:
        super().watch_scroll_y(old_value, new_value)
        if round(new_value) >= self.max_scroll_y:
            self._set_following_end(True)
        elif new_value < old_value:
            self._set_following_end(False)

    def _should_scroll_end(self, scroll_end: bool | None) -> bool:
        """Should a write scroll to the end?

        Args:
            scroll_end: The `scroll_end` argument given to `write`.
        """
        if scroll_end is not None:
            return scroll_end
        return (
            self.auto_scroll
            and self._following_end
            and not self.is_vertical_scrollbar_grabbed
        )

    def _scroll_end_after_refresh(self, animate: bool, force: bool) -> None:
        """Scroll to the end after the next refresh.

        Args:
            animate: Animate the scroll.
            force: Scroll even if the log stopped following in the meantime.
        """

        def scroll_end() -> None:
            if force or self._following_end:
                self.scroll_end(animate=animate, immediate=True, x_axis=False)

        self.call_after_refresh(scroll_end)

    def _keep_viewport(self, removed_lines: int) -> None:
        """Keep the same content in view after lines were removed from the top.

        Args:
            removed_lines: Number of lines removed from the top of the log.
        """
        if removed_lines:
            self.scroll_target_y = self.scroll_y = max(
                0, self.scroll_y - removed_lines
            )
            self.refresh()

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
        else:
            self._rerender_expanded()
        if self.auto_scroll and self._following_end:
            self._scroll_end_after_refresh(animate=False, force=False)

    def watch_min_width(self) -> None:
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

    def _get_render_options(self, renderable: RenderableType) -> ConsoleOptions:
        """Get the console options used to render a renderable.

        Args:
            renderable: Renderable to be rendered.
        """
        render_options = self.app.console.options
        if isinstance(renderable, Text) and not self.wrap:
            render_options = render_options.update(overflow="ignore", no_wrap=True)
        return render_options

    def _get_render_width(
        self,
        renderable: RenderableType,
        render_options: ConsoleOptions,
        width: int | None,
        expand: bool,
        shrink: bool,
    ) -> int:
        """Get the width a renderable should be rendered at.

        Args:
            renderable: Renderable to be rendered.
            render_options: Console options used to render.
            width: Width requested by the caller, or `None` to compute it.
            expand: Permit expanding to the width of the content region.
            shrink: Permit shrinking to fit within the content region.
        """
        if width is not None:
            # Use the width specified by the caller.
            # We ignore `expand` and `shrink` when a width is specified.
            # This also overrides `min_width` set on the RichLog.
            return width

        # Compute the width based on available information.
        renderable_width = measure_renderables(
            self.app.console, render_options, [renderable]
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

    def _justify_text(
        self, text: Text, width: int, justify: JustifyMethod | None
    ) -> Text:
        """Justify unwrapped text to a given width.

        Rich doesn't justify text rendered with `overflow="ignore"`, so we do it here.

        Args:
            text: Text to justify.
            width: Width to justify to.
            justify: Justify method from the console options.

        Returns:
            Justified text.
        """
        justify = text.justify or justify
        if justify in (None, "default"):
            return text
        lines = text.split(allow_blank=True)
        lines.justify(self.app.console, width, justify=justify, overflow="ignore")
        justified = Text("\n").join(lines)
        justified.overflow = text.overflow
        justified.no_wrap = text.no_wrap
        justified.end = text.end
        return justified

    def _render_strips(
        self,
        renderable: RenderableType,
        render_options: ConsoleOptions,
        render_width: int,
    ) -> tuple[list[Strip], int]:
        """Render a renderable into strips.

        Args:
            renderable: Renderable to render.
            render_options: Console options used to render.
            render_width: Width to render at.

        Returns:
            The rendered strips, and the width of the widest line.
        """
        if (
            isinstance(renderable, Text)
            and (renderable.overflow or render_options.overflow) == "ignore"
        ):
            renderable = self._justify_text(
                renderable, render_width, render_options.justify
            )
        render_options = render_options.update_width(render_width)

        # Render into (possibly) wrapped lines.
        segments = self.app.console.render(renderable, render_options)
        lines = list(Segment.split_lines(segments))

        if not lines:
            return [Strip.blank(render_width)], render_width

        strips = Strip.from_lines(lines)
        for strip in strips:
            strip.adjust_cell_length(render_width)
        widest = max(sum([segment.cell_length for segment in _line]) for _line in lines)
        return strips, widest

    def _prune_max_lines(self) -> int:
        """Remove lines from the top if there are more than `max_lines`.

        Returns:
            The number of lines removed.
        """
        if self.max_lines is None or len(self.lines) <= self.max_lines:
            return 0
        removed = len(self.lines) - self.max_lines
        self._start_line += removed
        self.lines = self.lines[-self.max_lines :]
        entries = self._entries
        remaining = removed
        while entries and remaining >= entries[0].line_count:
            remaining -= entries.popleft().line_count
        if entries and remaining:
            entries[0].line_count -= remaining
            # A partially removed entry can no longer be re-rendered as a whole.
            entries[0].renderable = None
        self.refresh()
        return removed

    def _rerender_expanded(self) -> None:
        """Re-render expanded entries whose render width has changed."""
        if not self._size_known or not any(
            entry.renderable is not None for entry in self._entries
        ):
            return
        new_lines: list[Strip] = []
        widest = 0
        offset = 0
        changed = False
        for entry in self._entries:
            strips = self.lines[offset : offset + entry.line_count]
            offset += entry.line_count
            if entry.renderable is not None:
                render_options = self._get_render_options(entry.renderable)
                render_width = self._get_render_width(
                    entry.renderable, render_options, None, True, entry.shrink
                )
                if render_width != entry.render_width:
                    strips, entry.widest = self._render_strips(
                        entry.renderable, render_options, render_width
                    )
                    entry.render_width = render_width
                    entry.line_count = len(strips)
                    changed = True
            new_lines.extend(strips)
            widest = max(widest, entry.widest)
        if not changed:
            return
        self.lines = new_lines
        self._widest_line_width = widest
        self._line_cache.clear()
        removed = self._prune_max_lines()
        self.virtual_size = Size(self._widest_line_width, len(self.lines))
        if not (self.auto_scroll and self._following_end):
            self._keep_viewport(removed)
        self.refresh()

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

        Args:
            content: Rich renderable (or a string).
            width: Width to render, or `None` to use `RichLog.min_width`.
                If specified, `expand` and `shrink` will be ignored.
            expand: Permit expanding of content to the width of the content region of the RichLog.
                If `width` is specified, then `expand` will be ignored.
            shrink: Permit shrinking of content to fit within the content region of the RichLog.
                If `width` is specified, then `shrink` will be ignored.
            scroll_end: Enable automatic scroll to end, or `None` to use `self.auto_scroll`
                (which only scrolls if the log is following the end).
            animate: Enable animation if the log will scroll.

        Returns:
            The `RichLog` instance.
        """
        if not self._size_known:
            # We don't know the size yet, so we'll need to render this later.
            # We defer ALL writes until the size is known, to ensure ordering is preserved.
            if isinstance(content, Text):
                content = content.copy()
            self._deferred_renders.append(
                DeferredRender(content, width, expand, shrink, scroll_end)
            )
            return self

        renderable = self._make_renderable(content)
        auto_scroll = self._should_scroll_end(scroll_end)

        render_options = self._get_render_options(renderable)
        render_width = self._get_render_width(
            renderable, render_options, width, expand, shrink
        )
        strips, widest = self._render_strips(renderable, render_options, render_width)

        rerender = expand and width is None
        if rerender and isinstance(renderable, Text) and renderable is content:
            renderable = renderable.copy()
        self._entries.append(
            _LogEntry(
                len(strips),
                widest,
                render_width,
                renderable if rerender else None,
                shrink,
            )
        )
        self.lines.extend(strips)
        removed = self._prune_max_lines()

        # Compute the width after wrapping and trimming
        # TODO - this is wrong because if we trim a long line, the max width
        #  could decrease, but we don't look at which lines were trimmed here.
        self._widest_line_width = max(self._widest_line_width, widest)

        # Update the virtual size - the width may have changed after adding
        # the new line(s), and the height will definitely have changed.
        self.virtual_size = Size(self._widest_line_width, len(self.lines))

        if auto_scroll:
            self._scroll_end_after_refresh(
                animate=animate, force=scroll_end is not None
            )
        else:
            self._keep_viewport(removed)

        return self

    def clear(self) -> Self:
        """Clear the text log.

        Returns:
            The `RichLog` instance.
        """
        self.lines.clear()
        self._line_cache.clear()
        self._entries.clear()
        self._start_line = 0
        self._widest_line_width = 0
        self._deferred_renders.clear()
        self.virtual_size = Size(0, len(self.lines))
        self._set_following_end(True)
        self.refresh()
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

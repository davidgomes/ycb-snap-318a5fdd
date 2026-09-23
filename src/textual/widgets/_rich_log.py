"""Provides a scrollable text-logging widget."""

from __future__ import annotations

from collections import deque
from typing import TYPE_CHECKING, NamedTuple, Optional, cast

from rich.console import ConsoleOptions, RenderableType
from rich.containers import Lines
from rich.highlighter import Highlighter, ReprHighlighter
from rich.measure import measure_renderables
from rich.pretty import Pretty
from rich.protocol import is_renderable
from rich.segment import Segment
from rich.text import Text

from textual.cache import LRUCache
from textual.events import Resize
from textual.geometry import Size
from textual.reactive import var
from textual.strip import Strip
from textual.widgets._follow_end_scroll_view import FollowEndScrollView

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


class _ExpandedEntry:
    """Lines written with `expand=True`, which are re-rendered when the width changes."""

    __slots__ = ["renderable", "shrink", "start", "line_count", "render_width"]

    def __init__(
        self,
        renderable: RenderableType,
        shrink: bool,
        start: int,
        line_count: int,
        render_width: int,
    ) -> None:
        self.renderable = renderable
        """The renderable which was written."""
        self.shrink = shrink
        """Enable shrinking of content to fit width."""
        self.start = start
        """Index of the first line, including lines removed by `max_lines`."""
        self.line_count = line_count
        """The number of lines rendered."""
        self.render_width = render_width
        """The width the lines were rendered at."""


class RichLog(FollowEndScrollView, can_focus=True):
    """A widget for logging Rich renderables and text."""

    class FollowChanged(FollowEndScrollView.FollowChanged):
        """Posted when the log starts or stops following the end of its content.

        Can be handled using `on_rich_log_follow_changed` in a subclass of `RichLog`
        or in a parent widget in the DOM.
        """

        widget: RichLog
        """The log which started or stopped following the end."""

        @property
        def control(self) -> RichLog:
            """An alias for [FollowChanged.widget][textual.widgets.RichLog.FollowChanged.widget]."""
            return self.widget

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
        self.max_lines = max_lines
        """Maximum number of lines in the log or `None` for no maximum."""
        self._start_line: int = 0
        self.lines: list[Strip] = []
        """The lines currently visible in the log."""
        self._line_cache: LRUCache[tuple[int, int, int, int], Strip]
        self._line_cache = LRUCache(1024)
        self._deferred_renders: deque[DeferredRender] = deque()
        """Queue of deferred renderables to be rendered."""
        self._expanded_entries: deque[_ExpandedEntry] = deque()
        """Entries written with `expand=True`, in the order they were written."""
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

        self._size_known = False
        """Flag which is set to True when the size of the RichLog is known,
        indicating we can proceed with rendering deferred writes."""

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
        elif self._size_known:
            self._rerender_expanded_entries()

    def _watch_min_width(self) -> None:
        if self.is_mounted and self._size_known:
            self._rerender_expanded_entries()

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
            scroll_end: Enable automatic scroll to end, or `None` to scroll only if
                `self.auto_scroll` is enabled and the log is following the end.
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
        follow = self._should_follow_write(scroll_end)
        render_options = self._get_render_options(renderable)

        if width is not None:
            # Use the width specified by the caller.
            # We ignore `expand` and `shrink` when a width is specified.
            # This also overrides `min_width` set on the RichLog.
            render_width = width
        else:
            render_width = self._get_render_width(
                renderable, render_options, expand, shrink
            )

        strips = self._render_strips(renderable, render_options, render_width)
        if expand and width is None:
            self._expanded_entries.append(
                _ExpandedEntry(
                    renderable.copy() if isinstance(renderable, Text) else renderable,
                    shrink,
                    self._start_line + len(self.lines),
                    len(strips),
                    render_width,
                )
            )
        self.lines.extend(strips)
        removed_lines = self._prune_max_lines()

        # Compute the width after wrapping and trimming
        # TODO - this is wrong because if we trim a long line, the max width
        #  could decrease, but we don't look at which lines were trimmed here.
        self._widest_line_width = max(
            self._widest_line_width, max(strip.cell_length for strip in strips)
        )

        # Update the virtual size - the width may have changed after adding
        # the new line(s), and the height will definitely have changed.
        self.virtual_size = Size(self._widest_line_width, len(self.lines))

        self._scroll_after_write(
            follow, removed_lines, immediate=False, animate=animate
        )
        return self

    def _get_render_options(self, renderable: RenderableType) -> ConsoleOptions:
        """Get the console options used to render a renderable.

        Args:
            renderable: A Rich renderable.

        Returns:
            Console options.
        """
        render_options = self.app.console.options
        if isinstance(renderable, Text) and not self.wrap:
            render_options = render_options.update(overflow="ignore", no_wrap=True)
        return render_options

    def _get_render_width(
        self,
        renderable: RenderableType,
        render_options: ConsoleOptions,
        expand: bool,
        shrink: bool,
    ) -> int:
        """Get the width to render a renderable, when no width was given to `write`.

        Args:
            renderable: A Rich renderable.
            render_options: Console options from `_get_render_options`.
            expand: Permit expanding of content to the width of the content region.
            shrink: Permit shrinking of content to fit within the content region.

        Returns:
            The width to render at.
        """
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

    def _render_strips(
        self,
        renderable: RenderableType,
        render_options: ConsoleOptions,
        render_width: int,
    ) -> list[Strip]:
        """Render a renderable into lines.

        Args:
            renderable: A Rich renderable.
            render_options: Console options from `_get_render_options`.
            render_width: The width to render at.

        Returns:
            One or more strips.
        """
        render_options = render_options.update_width(render_width)
        if isinstance(renderable, Text):
            renderable = self._justify_text(renderable, render_options)

        # Render into (possibly) wrapped lines.
        segments = self.app.console.render(renderable, render_options)
        lines = list(Segment.split_lines(segments))
        if not lines:
            return [Strip.blank(render_width)]

        strips = Strip.from_lines(lines)
        for strip in strips:
            strip.adjust_cell_length(render_width)
        return strips

    def _justify_text(self, text: Text, render_options: ConsoleOptions) -> Text:
        """Justify text which won't be wrapped.

        Rich doesn't justify text when the overflow is "ignore", which is used to
        prevent unwrapped lines from being cropped.

        Args:
            text: Text to render.
            render_options: Console options with the render width.

        Returns:
            Justified text, or the original text if Rich will justify it.
        """
        justify = text.justify or render_options.justify
        overflow = text.overflow or render_options.overflow
        if overflow != "ignore" or justify in (None, "default"):
            return text

        console = self.app.console
        lines = Lines()
        for line in text.split(allow_blank=True):
            # Each line is justified on its own, as Rich does for text that isn't wrapped.
            line_lines = Lines([line])
            line_lines.justify(
                console, render_options.max_width, justify=justify, overflow=overflow
            )
            lines.extend(line_lines)

        separator = Text(
            "\n",
            justify="default",
            overflow=text.overflow,
            no_wrap=text.no_wrap,
            end=text.end,
            tab_size=text.tab_size,
        )
        return separator.join(lines)

    def _prune_max_lines(self) -> int:
        """Remove lines from the start of the log if there are more than `max_lines`.

        Returns:
            The number of lines removed.
        """
        if self.max_lines is None or len(self.lines) <= self.max_lines:
            return 0
        removed_lines = len(self.lines) - self.max_lines
        self._start_line += removed_lines
        self.refresh()
        self.lines = self.lines[removed_lines:]

        # Entries which have lost lines can no longer be re-rendered.
        expanded_entries = self._expanded_entries
        while expanded_entries and expanded_entries[0].start < self._start_line:
            expanded_entries.popleft()
        return removed_lines

    def _rerender_expanded_entries(self) -> None:
        """Re-render entries written with `expand=True`, to fit the current width."""
        if not self._expanded_entries or not self.scrollable_content_region.width:
            return

        lines = self.lines
        first_visible_line = self.scroll_offset.y
        new_lines: list[Strip] = []
        position = 0
        line_shift = 0
        visible_line_shift = 0

        for entry in self._expanded_entries:
            entry.start += line_shift
            render_options = self._get_render_options(entry.renderable)
            render_width = self._get_render_width(
                entry.renderable, render_options, True, entry.shrink
            )
            if render_width == entry.render_width:
                continue

            strips = self._render_strips(entry.renderable, render_options, render_width)
            index = entry.start - line_shift - self._start_line
            new_lines.extend(lines[position:index])
            new_lines.extend(strips)
            position = index + entry.line_count

            # Track where the first visible line moves to, to keep the viewport stable.
            line_count_change = len(strips) - entry.line_count
            visible_offset = first_visible_line - index
            if visible_offset >= entry.line_count:
                visible_line_shift += line_count_change
            elif visible_offset >= 0:
                new_visible_offset = min(visible_offset, len(strips) - 1)
                visible_line_shift += new_visible_offset - visible_offset
            line_shift += line_count_change
            entry.line_count = len(strips)
            entry.render_width = render_width

        if not position:
            return

        new_lines.extend(lines[position:])
        self.lines = new_lines
        self._line_cache.clear()
        removed_lines = self._prune_max_lines()
        self._widest_line_width = max(
            (strip.cell_length for strip in self.lines), default=0
        )
        self.virtual_size = Size(self._widest_line_width, len(self.lines))
        self.refresh()

        if self.auto_scroll and self._following_end:
            self._scroll_end_after_refresh()
        else:
            scroll_y = max(0, first_visible_line + visible_line_shift - removed_lines)
            if scroll_y != first_visible_line:
                self.scroll_target_y = scroll_y
                self.scroll_y = scroll_y

    def clear(self) -> Self:
        """Clear the text log.

        Returns:
            The `RichLog` instance.
        """
        self.lines.clear()
        self._line_cache.clear()
        self._start_line = 0
        self._widest_line_width = 0
        self._deferred_renders.clear()
        self._expanded_entries.clear()
        self.virtual_size = Size(0, len(self.lines))
        self.refresh()
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

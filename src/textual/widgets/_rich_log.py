"""Provides a scrollable text-logging widget."""

from __future__ import annotations

from collections import deque
from typing import TYPE_CHECKING, NamedTuple, Optional, cast

from rich.console import RenderableType
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
from textual.scroll_view import ScrollView
from textual.strip import Strip
from textual.widgets._log_follow import FollowChangedMessage, FollowEndMixin

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
    """Bookkeeping for a single call to `RichLog.write`."""

    __slots__ = ["renderable", "shrink", "line_count", "pruned", "render_key"]

    def __init__(
        self,
        renderable: RenderableType | None,
        shrink: bool,
        line_count: int,
        render_key: tuple[int, int],
    ) -> None:
        self.renderable = renderable
        """The renderable, retained only for expanded entries which may be re-rendered."""
        self.shrink = shrink
        """Shrink setting used when writing."""
        self.line_count = line_count
        """Number of lines of this entry currently in the log."""
        self.pruned = 0
        """Number of leading lines of this entry removed due to `max_lines`."""
        self.render_key = render_key
        """The (content width, min width) this entry was last rendered with."""


class RichLog(FollowEndMixin, ScrollView, can_focus=True):
    """A widget for logging Rich renderables and text."""

    class FollowChanged(FollowChangedMessage):
        """Posted when [`is_following_end`][textual.widgets.RichLog.is_following_end] changes."""

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
        else:
            self._rerender_expanded()

    def watch_min_width(self) -> None:
        if self.is_mounted:
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
        expand = expand and width is None
        render_key = self._render_key
        strips, widest_line_width = self._render_strips(
            renderable, width, expand, shrink
        )
        self.lines.extend(strips)
        if expand and isinstance(renderable, Text):
            renderable = renderable.copy()
        self._entries.append(
            _LogEntry(renderable if expand else None, shrink, len(strips), render_key)
        )
        removed_lines = self._prune_max_lines()

        # Compute the width after wrapping and trimming
        # TODO - this is wrong because if we trim a long line, the max width
        #  could decrease, but we don't look at which lines were trimmed here.
        self._widest_line_width = max(self._widest_line_width, widest_line_width)

        # Update the virtual size - the width may have changed after adding
        # the new line(s), and the height will definitely have changed.
        self.virtual_size = Size(self._widest_line_width, len(self.lines))

        self._after_write(scroll_end, removed_lines, animate=animate, immediate=False)
        return self

    @property
    def _render_key(self) -> tuple[int, int]:
        """The widths which determine how expanded content is rendered."""
        return (self.scrollable_content_region.width, self.min_width)

    def _render_strips(
        self,
        renderable: RenderableType,
        width: int | None,
        expand: bool,
        shrink: bool,
    ) -> tuple[list[Strip], int]:
        """Render a renderable into strips.

        Args:
            renderable: A Rich renderable.
            width: Width to render, or `None` to calculate from the content and widget.
            expand: Permit expanding of content to the width of the content region.
            shrink: Permit shrinking of content to fit within the content region.

        Returns:
            The rendered strips, and the width of the widest rendered line.
        """
        console = self.app.console
        render_options = console.options

        no_wrap_text = isinstance(renderable, Text) and not self.wrap
        if no_wrap_text:
            render_options = render_options.update(overflow="ignore", no_wrap=True)

        if width is not None:
            # Use the width specified by the caller.
            # We ignore `expand` and `shrink` when a width is specified.
            # This also overrides `min_width` set on the RichLog.
            render_width = width
        else:
            # Compute the width based on available information.
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
            render_width = max(render_width, self.min_width)

        render_options = render_options.update_width(render_width)
        if no_wrap_text:
            # Rich skips justification when overflow is "ignore", so crop instead,
            # at a width which fits the longest line so that nothing is cropped.
            assert isinstance(renderable, Text)
            text_width = renderable.__rich_measure__(console, render_options).maximum
            render_options = render_options.update(
                width=max(render_width, text_width), overflow="crop"
            )

        # Render into (possibly) wrapped lines.
        segments = console.render(renderable, render_options)
        lines = list(Segment.split_lines(segments))

        if not lines:
            return [Strip.blank(render_width)], render_width

        strips = Strip.from_lines(lines)
        for strip in strips:
            strip.adjust_cell_length(render_width)
        widest_line_width = max(
            sum([segment.cell_length for segment in _line]) for _line in lines
        )
        return strips, widest_line_width

    def _prune_max_lines(self) -> int:
        """Remove lines from the start of the log in excess of `max_lines`.

        Returns:
            The number of lines removed.
        """
        if self.max_lines is None or len(self.lines) <= self.max_lines:
            return 0
        removed_lines = len(self.lines) - self.max_lines
        self._start_line += removed_lines
        del self.lines[:removed_lines]

        entries = self._entries
        remaining = removed_lines
        while remaining and entries:
            entry = entries[0]
            if entry.line_count <= remaining:
                remaining -= entry.line_count
                entries.popleft()
            else:
                entry.line_count -= remaining
                entry.pruned += remaining
                remaining = 0

        self.refresh()
        return removed_lines

    def _rerender_expanded(self) -> None:
        """Re-render expanded entries whose available width has changed."""
        if not self._size_known:
            return
        render_key = self._render_key
        if not render_key[0]:
            return
        if not any(
            entry.renderable is not None and entry.render_key != render_key
            for entry in self._entries
        ):
            return

        lines = self.lines
        new_lines: list[Strip] = []
        offset = 0
        for entry in self._entries:
            line_count = entry.line_count
            if entry.renderable is not None and entry.render_key != render_key:
                strips, _ = self._render_strips(
                    entry.renderable, None, True, entry.shrink
                )
                strips = strips[entry.pruned :]
                entry.line_count = len(strips)
                entry.render_key = render_key
                new_lines.extend(strips)
            else:
                new_lines.extend(lines[offset : offset + line_count])
            offset += line_count

        self.lines = new_lines
        self._line_cache.clear()
        removed_lines = self._prune_max_lines()
        self._widest_line_width = max(
            (line.cell_length for line in self.lines), default=0
        )
        self.virtual_size = Size(self._widest_line_width, len(self.lines))
        self._after_write(
            self._following_end or None, removed_lines, immediate=False
        )
        self.refresh()

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
        self._entries.clear()
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

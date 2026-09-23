"""Provides a scrollable text-logging widget."""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from typing import TYPE_CHECKING, NamedTuple, Optional, cast

from rich.console import RenderableType
from rich.highlighter import Highlighter, ReprHighlighter
from rich.measure import measure_renderables
from rich.pretty import Pretty
from rich.protocol import is_renderable
from rich.segment import Segment
from rich.style import Style
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


@dataclass
class _LineGroup:
    """Strips produced by one `RichLog.write`, plus data needed to re-expand."""

    strips: list[Strip]
    content: RenderableType | object | None = None
    width: int | None = None
    expand: bool = False
    shrink: bool = True


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
        self._entries: list[_LineGroup] = []
        """Writes currently held in the log, in display order."""
        self._following_end: bool = True
        """Whether new writes should stick to the end while `auto_scroll` is on."""
        self._suspend_follow: bool = False
        self._expand_signature: tuple[int, int] | None = None
        """Width and min_width last used to render expanded writes."""
        self._size_known = False
        """Flag which is set to True when the size of the RichLog is known,
        indicating we can proceed with rendering deferred writes."""
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

    class FollowChanged(Message):
        """Posted when [`RichLog.is_following_end`][textual.widgets.RichLog.is_following_end] changes.

        This is not posted when the flag stays the same.
        """

        def __init__(
            self,
            widget: RichLog,
            is_following_end: bool,
            scroll_y: float,
            max_scroll_y: int,
        ) -> None:
            self.widget: RichLog = widget
            """The log whose follow state changed."""
            self.is_following_end: bool = is_following_end
            """Whether the log is now following the end."""
            self.scroll_y: float = scroll_y
            """Vertical scroll offset when the state changed."""
            self.max_scroll_y: int = max_scroll_y
            """Maximum vertical scroll offset when the state changed."""
            super().__init__()

        @property
        def control(self) -> RichLog:
            """The log associated with this message."""
            return self.widget

    @property
    def is_following_end(self) -> bool:
        """Whether the log is following newly written lines.

        Scrolling away from the end turns this off. Scrolling back to the end,
        or calling [`follow_end`][textual.widgets.RichLog.follow_end], turns it on.
        """
        return self._following_end

    def follow_end(self, animate: bool = False) -> None:
        """Scroll to the end and follow new writes.

        Args:
            animate: Animate the scroll.
        """
        self._set_following_end(True)
        self.scroll_end(animate=animate, immediate=not animate, x_axis=False)

    def watch_scroll_y(self, old_value: float, new_value: float) -> None:
        super().watch_scroll_y(old_value, new_value)
        if getattr(self, "_suspend_follow", True):
            return
        at_end = self.is_vertical_scroll_end
        if at_end:
            self._set_following_end(True)
            return
        # An in-progress follow animation moves toward the end; don't cancel it.
        if (
            self._following_end
            and round(self.scroll_target_y) >= self.max_scroll_y
            and new_value > old_value
        ):
            return
        self._set_following_end(False)

    def watch_min_width(self, min_width: int) -> None:
        if getattr(self, "_entries", None) is None:
            return
        self._maybe_rerender_expanded()

    def _set_following_end(self, following: bool) -> None:
        """Update follow state and post [`FollowChanged`][textual.widgets.RichLog.FollowChanged]."""
        if following == self._following_end:
            return
        self._following_end = following
        self.post_message(
            self.FollowChanged(
                self,
                following,
                self.scroll_y,
                self.max_scroll_y,
            )
        )

    def _should_scroll_on_write(self, scroll_end: bool | None, following: bool) -> bool:
        """Return whether a write should scroll to the end."""
        if scroll_end is not None:
            return scroll_end
        return self.auto_scroll and following and not self.is_vertical_scrollbar_grabbed

    def _shift_scroll_y(self, delta: float) -> None:
        """Move the viewport without changing follow state."""
        if not delta:
            return
        self._suspend_follow = True
        try:
            new_y = max(0.0, self.scroll_y + delta)
            self.scroll_target_y = self.scroll_y = new_y
        finally:
            self._suspend_follow = False

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
            content_width = self.scrollable_content_region.width
            if content_width > 0:
                self._expand_signature = (content_width, self.min_width)
        else:
            self._maybe_rerender_expanded()

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
            scroll_end: Enable automatic scroll to end, or `None` to use `self.auto_scroll`.
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

        following = self.is_following_end
        strips = self._render_strips(content, width, expand, shrink)
        expandable = expand and width is None
        self._entries.append(
            _LineGroup(
                strips=list(strips),
                content=self._copy_content(content) if expandable else None,
                width=width,
                expand=expandable,
                shrink=shrink,
            )
        )
        self.lines.extend(strips)
        if expandable:
            content_width = self.scrollable_content_region.width
            if content_width > 0:
                self._expand_signature = (content_width, self.min_width)
        removed = self._prune_lines()
        if removed and not following:
            self._shift_scroll_y(-removed)
        self._update_virtual_size()

        if self._should_scroll_on_write(scroll_end, following):
            self.scroll_end(animate=animate, immediate=False, x_axis=False)
        else:
            self.refresh()

        return self

    def _copy_content(
        self, content: RenderableType | object
    ) -> RenderableType | object:
        """Copy content that would be mutated by a later render."""
        if isinstance(content, Text):
            return content.copy()
        return content

    def _render_strips(
        self,
        content: RenderableType | object,
        width: int | None,
        expand: bool,
        shrink: bool,
    ) -> list[Strip]:
        """Render one write into strips.

        Args:
            content: Rich renderable, or an object pretty-printed.
            width: Explicit width, or `None` to measure.
            expand: Expand to the log's content width when `width` is `None`.
            shrink: Shrink to the log's content width when `width` is `None`.

        Returns:
            Rendered strips, padded to the chosen width.
        """
        renderable = self._make_renderable(content)

        console = self.app.console
        render_options = console.options

        if isinstance(renderable, Text) and not self.wrap:
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

        if expand and width is None and isinstance(renderable, Text) and not self.wrap:
            # Rich skips justification when overflow is "ignore", so expanded
            # text would lose right/center/full alignment and its background.
            render_options = render_options.update(overflow="crop")

        render_options = render_options.update_width(render_width)

        # Render into (possibly) wrapped lines.
        segments = console.render(renderable, render_options)
        lines = list(Segment.split_lines(segments))

        pad_style = self._pad_style(renderable)
        if not lines:
            return [Strip.blank(render_width, pad_style)]

        # Expanded and explicit widths are exact. Other writes only grow to
        # min_width so overflow="ignore" lines are not cropped.
        fit = (
            Strip.adjust_cell_length
            if width is not None or expand
            else Strip.extend_cell_length
        )
        return [
            fit(strip, render_width, pad_style) for strip in Strip.from_lines(lines)
        ]

    def _pad_style(self, renderable: RenderableType) -> Style | None:
        """Style to use when padding a strip out to the render width."""
        if not isinstance(renderable, Text):
            return None
        style = renderable.style
        if isinstance(style, Style):
            return style or None
        if isinstance(style, str) and style:
            return Style.parse(style)
        return None

    def _prune_lines(self) -> int:
        """Drop lines above `max_lines`.

        Returns:
            How many lines were removed from the top.
        """
        if self.max_lines is None or len(self.lines) <= self.max_lines:
            return 0
        remove = len(self.lines) - self.max_lines
        self._start_line += remove
        self.lines = self.lines[-self.max_lines :]
        self._drop_entry_lines(remove)
        self._line_cache.clear()
        return remove

    def _drop_entry_lines(self, count: int) -> None:
        """Remove `count` strips from the front of `_entries`."""
        remaining = count
        while self._entries and remaining:
            entry = self._entries[0]
            have = len(entry.strips)
            if have <= remaining:
                remaining -= have
                del self._entries[0]
            else:
                del entry.strips[:remaining]
                # The write was sliced, so it can no longer be re-expanded.
                entry.expand = False
                entry.content = None
                remaining = 0

    def _update_virtual_size(self) -> None:
        """Refresh cached width and virtual size from the current lines."""
        width = 0
        for line in self.lines:
            width = max(width, line.cell_length)
        self._widest_line_width = width
        self.virtual_size = Size(width, len(self.lines))

    def _scroll_anchor(self) -> tuple[int, int]:
        """Return the entry index and line offset at the top of the viewport."""
        y = int(self.scroll_offset.y)
        offset = 0
        for index, entry in enumerate(self._entries):
            height = len(entry.strips)
            if y < offset + height:
                return index, y - offset
            offset += height
        return max(0, len(self._entries) - 1), 0

    def _y_for_anchor(self, anchor: tuple[int, int]) -> int:
        """Translate an entry anchor back into a scroll offset."""
        index, inner = anchor
        if index >= len(self._entries):
            index = max(0, len(self._entries) - 1)
            inner = 0
        y = 0
        for entry_index, entry in enumerate(self._entries):
            if entry_index == index:
                if not entry.strips:
                    return y
                return y + min(inner, len(entry.strips) - 1)
            y += len(entry.strips)
        return y

    def _maybe_rerender_expanded(self) -> None:
        """Re-render expanded writes when the content width or `min_width` changes."""
        if not self._size_known or not self._entries:
            return
        if not any(entry.expand and entry.width is None for entry in self._entries):
            return
        width = self.scrollable_content_region.width
        if width <= 0:
            return
        signature = (width, self.min_width)
        if signature == self._expand_signature:
            return
        self._expand_signature = signature
        self._rerender_expanded()

    def _rerender_expanded(self) -> None:
        """Rebuild expanded lines so justification tracks the current width."""
        anchor = self._scroll_anchor()
        following = self.is_following_end
        for entry in self._entries:
            if entry.expand and entry.width is None and entry.content is not None:
                entry.strips = self._render_strips(
                    entry.content, None, True, entry.shrink
                )
        self.lines = [strip for entry in self._entries for strip in entry.strips]
        anchored_y = self._y_for_anchor(anchor)
        removed = self._prune_lines()
        self._line_cache.clear()
        self._update_virtual_size()
        if following and self.auto_scroll:
            self.scroll_end(animate=False, immediate=False, x_axis=False)
        else:
            self._shift_scroll_y(max(0, anchored_y - removed) - self.scroll_y)
            self.refresh()

    def clear(self) -> Self:
        """Clear the text log.

        Returns:
            The `RichLog` instance.
        """
        self.lines.clear()
        self._entries.clear()
        self._line_cache.clear()
        self._start_line = 0
        self._widest_line_width = 0
        self._expand_signature = None
        self._deferred_renders.clear()
        self.virtual_size = Size(0, len(self.lines))
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

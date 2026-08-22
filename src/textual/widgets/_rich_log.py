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
        """Posted when the log starts or stops following its end."""

        def __init__(self, widget: RichLog) -> None:
            self.widget = widget
            self.is_following_end = widget.is_following_end
            self.scroll_y = widget.scroll_y
            self.max_scroll_y = widget.max_scroll_y
            super().__init__()

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
        self._entries: list[DeferredRender] = []
        self._is_rebuilding = False
        self._is_following_end = True

    @property
    def is_following_end(self) -> bool:
        """Whether the log is scrolled to the end."""
        return self._is_following_end

    def _update_follow_state(self, is_following_end: bool | None = None) -> None:
        if is_following_end is None:
            is_following_end = self.is_vertical_scroll_end
        if is_following_end != self._is_following_end:
            self._is_following_end = is_following_end
            self.post_message(self.FollowChanged(self))

    def follow_end(self, animate: bool = False) -> Self:
        """Scroll to the end and follow new content."""
        self.scroll_end(animate=animate, immediate=True, x_axis=False)
        self._update_follow_state(True)
        return self

    def watch_scroll_y(self, old_value: float, new_value: float) -> None:
        super().watch_scroll_y(old_value, new_value)
        self._update_follow_state()

    def notify_style_update(self) -> None:
        super().notify_style_update()
        self._line_cache.clear()

    def watch_min_width(self, old_width: int, new_width: int) -> None:
        if new_width != old_width and getattr(self, "_size_known", False):
            self._rerender_entries()

    def on_resize(self, event: Resize) -> None:
        if event.size.width and not self._size_known:
            # This size is known for the first time.
            self._size_known = True
            deferred_renders = self._deferred_renders
            while deferred_renders:
                deferred_render = deferred_renders.popleft()
                self.write(*deferred_render)
        elif self._size_known:
            self._rerender_entries()

    def _rerender_entries(self) -> None:
        """Re-render entries whose width depends on the current widget size."""
        if self._is_rebuilding or not self._entries:
            return
        is_following_end = self.is_following_end
        scroll_y = self.scroll_y
        self._is_rebuilding = True
        self.lines.clear()
        self._line_cache.clear()
        self._start_line = 0
        self._widest_line_width = 0
        try:
            for entry in self._entries:
                self.write(*entry)
        finally:
            self._is_rebuilding = False
        if not is_following_end:
            self.scroll_y = min(scroll_y, self.max_scroll_y)
        self._update_follow_state()

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

        if not self._is_rebuilding:
            if isinstance(content, Text):
                content = content.copy()
            self._entries.append(DeferredRender(content, width, expand, shrink, scroll_end))

        is_following_end = self.is_following_end
        old_scroll_y = self.scroll_y
        renderable = self._make_renderable(content)
        auto_scroll = self.auto_scroll if scroll_end is None else scroll_end

        console = self.app.console
        render_options = console.options

        if (
            isinstance(renderable, Text)
            and not self.wrap
            and not (expand and width is None)
        ):
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

        # Render into (possibly) wrapped lines.
        segments = self.app.console.render(renderable, render_options)
        lines = list(Segment.split_lines(segments))

        if not lines:
            self._widest_line_width = max(render_width, self._widest_line_width)
            self.lines.append(Strip.blank(render_width))
        else:
            strips = Strip.from_lines(lines)
            for strip in strips:
                strip.adjust_cell_length(render_width)
            self.lines.extend(strips)

            removed_lines = 0
            if self.max_lines is not None and len(self.lines) > self.max_lines:
                removed_lines = len(self.lines) - self.max_lines
                self._start_line += removed_lines
                self.refresh()
                self.lines = self.lines[-self.max_lines :]

            # Compute the width after wrapping and trimming
            # TODO - this is wrong because if we trim a long line, the max width
            #  could decrease, but we don't look at which lines were trimmed here.
            self._widest_line_width = max(
                self._widest_line_width,
                max(sum([segment.cell_length for segment in _line]) for _line in lines),
            )

        if not lines:
            removed_lines = 0

        # Update the virtual size - the width may have changed after adding
        # the new line(s), and the height will definitely have changed.
        self.virtual_size = Size(self._widest_line_width, len(self.lines))

        if auto_scroll and (scroll_end is True or is_following_end):
            self.scroll_end(animate=animate, immediate=False, x_axis=False)
        elif removed_lines and not is_following_end:
            self.scroll_y = max(0, old_scroll_y - removed_lines)
        self._update_follow_state()

        return self

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
        self.refresh()
        self._update_follow_state()
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

"""Provides a scrollable text-logging widget."""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from typing import TYPE_CHECKING, NamedTuple, Optional, cast

from rich.cells import cell_len
from rich.console import Console, RenderableType
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
from textual.message import Message
from textual.reactive import var
from textual.scroll_view import ScrollView
from textual.strip import Strip
from textual.widgets._follow_end import (
    apply_write_scroll,
    capture_write_follow,
    init_follow_state,
    request_follow_end,
    scroll_end_if_following,
    set_following_end,
    suspend_follow_sync,
    sync_follow_on_scroll,
)

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


def _pad_justified_text(text: Text, width: int, console: Console) -> Text:
    """Pad justified text out to ``width``.

    Rich 14.3 skips justification when overflow is ``ignore``. RichLog uses
    that overflow so long lines can scroll horizontally, and previously
    relied on justification still padding short lines to the render width.

    Args:
        text: Text that may carry a justify method.
        width: Width to justify within.
        console: Console used by full justification.

    Returns:
        Text padded to ``width`` when it has a justify method, otherwise
        the original text.
    """
    justify = text.justify
    if width <= 0 or justify is None or justify == "default":
        return text

    pieces: list[Text] = []
    for line in text.split(allow_blank=True):
        line = line.copy()
        if justify != "full" and cell_len(line.plain) > width:
            pieces.append(line)
            continue
        group = Lines([line])
        group.justify(console, width, justify=justify, overflow="crop")
        pieces.extend(group)
    if not pieces:
        return text
    joined = Text("\n").join(pieces)
    # Justification is already represented by padding, so a later render
    # with overflow="ignore" must not try to justify again.
    joined.justify = None
    return joined


@dataclass
class _RichLogEntry:
    """One write, retained so expanded lines can be rendered again."""

    strips: list[Strip]
    reflow: bool = False
    content: RenderableType | object | None = None
    shrink: bool = True
    skip_lines: int = 0


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
    """Scroll to the end on write when the log is already following the end."""

    _following_end = True
    _suspend_follow_sync = False

    class FollowChanged(Message):
        """Posted when the end-follow state changes.

        The message is posted only when
        [`is_following_end`][textual.widgets.RichLog.is_following_end] changes.

        Can be handled using `on_rich_log_follow_changed` in a parent widget.
        """

        def __init__(
            self,
            widget: RichLog,
            is_following_end: bool,
            scroll_y: float,
            max_scroll_y: int,
        ) -> None:
            """Create a follow-changed message.

            Args:
                widget: The log whose follow state changed.
                is_following_end: The new follow state.
                scroll_y: Vertical scroll position when the state changed.
                max_scroll_y: Maximum vertical scroll position when the state changed.
            """
            self.widget = widget
            """The log whose follow state changed."""
            self.is_following_end = is_following_end
            """The new follow state."""
            self.scroll_y = scroll_y
            """Vertical scroll position when the state changed."""
            self.max_scroll_y = max_scroll_y
            """Maximum vertical scroll position when the state changed."""
            super().__init__()

        @property
        def control(self) -> RichLog:
            """The log whose follow state changed.

            This is an alias for
            [`FollowChanged.widget`][textual.widgets.RichLog.FollowChanged.widget].
            """
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
        init_follow_state(self)
        self._entries: list[_RichLogEntry] = []
        """Writes currently displayed, including enough data to reflow expanded lines."""
        self._expand_key: tuple[int, int] | None = None
        """Content width and min width used the last time expanded lines were rendered."""
        self._size_known = False
        """Flag which is set to True when the size of the RichLog is known,
        indicating we can proceed with rendering deferred writes."""
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
        """Scroll to the end on write when the log is already following the end."""
        self.highlighter: Highlighter = ReprHighlighter()
        """Rich Highlighter used to highlight content when highlight is True"""

        self._widest_line_width = 0
        """The width of the widest line currently in the log."""

    @property
    def is_following_end(self) -> bool:
        """Whether new writes should keep the view pinned to the end of the log.

        This starts as `True`. It becomes `False` when the user scrolls away
        from the end, and `True` again when the view returns to the end or
        [`follow_end`][textual.widgets.RichLog.follow_end] is called.

        While [`auto_scroll`][textual.widgets.RichLog.auto_scroll] is enabled,
        writes scroll to the end only when this is `True`.
        """
        return self._following_end

    def follow_end(self, animate: bool = False) -> None:
        """Scroll to the end of the log and follow later writes.

        Args:
            animate: Animate the scroll to the end.
        """
        request_follow_end(self, animate=animate)

    def watch_scroll_y(self, old_value: float, new_value: float) -> None:
        super().watch_scroll_y(old_value, new_value)
        sync_follow_on_scroll(self, old_value, new_value)

    def watch_min_width(self, old_value: int, new_value: int) -> None:
        if old_value != new_value:
            self._reflow_expanded()

    def _scroll_end_if_following(self, animate: bool = False) -> None:
        """Scroll to the end if this log is still following it."""
        scroll_end_if_following(self, animate)

    def notify_style_update(self) -> None:
        super().notify_style_update()
        self._line_cache.clear()

    def on_resize(self, event: Resize) -> None:
        if not event.size.width:
            return
        if not self._size_known:
            # This size is known for the first time.
            self._size_known = True
            deferred_renders = self._deferred_renders
            while deferred_renders:
                deferred_render = deferred_renders.popleft()
                self.write(*deferred_render)
        self._reflow_expanded()

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

    def _render_strips(
        self,
        content: RenderableType | object,
        width: int | None,
        expand: bool,
        shrink: bool,
    ) -> tuple[list[Strip], int]:
        """Render one write into strips.

        Args:
            content: Rich renderable, or an object to pretty-print.
            width: Width to render, or `None` to use the log width.
            expand: Expand content to the log width when `width` is `None`.
            shrink: Shrink content to the log width when `width` is `None`.

        Returns:
            The rendered strips and the width those strips contribute.
        """
        if isinstance(content, Text):
            # Copy so repeated renders (resize, min_width) don't mutate the
            # stored text, and so expand_tabs doesn't change the caller's object.
            content = content.copy()
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

        if isinstance(renderable, Text) and not self.wrap:
            renderable = _pad_justified_text(renderable, render_width, console)

        render_options = render_options.update_width(render_width)

        # Render into (possibly) wrapped lines.
        segments = self.app.console.render(renderable, render_options)
        lines = list(Segment.split_lines(segments))

        if not lines:
            return [Strip.blank(render_width)], render_width

        strips = Strip.from_lines(lines)
        widest = max(sum(segment.cell_length for segment in line) for line in lines)
        return strips, max(widest, render_width if expand and width is None else 0)

    def _expand_cache_key(self) -> tuple[int, int]:
        """Width inputs that change how expanded lines are rendered."""
        return (self.scrollable_content_region.width, self.min_width)

    def _prune_entries(self, count: int) -> None:
        """Drop ``count`` rendered lines from the start of the log."""
        if count <= 0:
            return
        self._start_line += count
        self._line_cache.clear()
        del self.lines[:count]
        remaining = count
        while remaining and self._entries:
            entry = self._entries[0]
            have = len(entry.strips)
            if have <= remaining:
                remaining -= have
                del self._entries[0]
            else:
                del entry.strips[:remaining]
                entry.skip_lines += remaining
                remaining = 0

    def _reflow_expanded(self) -> None:
        """Re-render expanded lines after a resize or ``min_width`` change."""
        if not self._size_known or not self._entries:
            return
        key = self._expand_cache_key()
        if key == self._expand_key:
            return
        if not any(entry.reflow for entry in self._entries):
            self._expand_key = key
            return

        scroll_y = self.scroll_y
        with suspend_follow_sync(self):
            widest = 0
            for entry in self._entries:
                if entry.reflow:
                    strips, width = self._render_strips(
                        entry.content, None, True, entry.shrink
                    )
                    if entry.skip_lines:
                        strips = strips[entry.skip_lines :]
                    entry.strips = strips
                else:
                    width = max(
                        (strip.cell_length for strip in entry.strips), default=0
                    )
                widest = max(widest, width)
            self.lines[:] = [strip for entry in self._entries for strip in entry.strips]
            self._widest_line_width = widest
            self._line_cache.clear()
            self._expand_key = key
            self.virtual_size = Size(self._widest_line_width, len(self.lines))
            self.refresh()
            if scroll_y != self.scroll_y or scroll_y != self.scroll_target_y:
                self.scroll_to(y=scroll_y, animate=False, immediate=True)

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
            scroll_end: Enable automatic scroll to end, or `None` to use
                `self.auto_scroll` while the log is following the end.
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

        follow, old_scroll_y = capture_write_follow(self, scroll_end)
        with suspend_follow_sync(self):
            strips, widest = self._render_strips(content, width, expand, shrink)
            reflow = bool(expand and width is None)
            stored: RenderableType | object | None = None
            if reflow:
                stored = content.copy() if isinstance(content, Text) else content
            self._entries.append(
                _RichLogEntry(
                    strips=list(strips),
                    reflow=reflow,
                    content=stored,
                    shrink=shrink,
                )
            )
            self.lines.extend(strips)
            removed = 0
            if self.max_lines is not None and len(self.lines) > self.max_lines:
                removed = len(self.lines) - self.max_lines
                self._prune_entries(removed)

            # Compute the width after wrapping and trimming
            # TODO - this is wrong because if we trim a long line, the max width
            #  could decrease, but we don't look at which lines were trimmed here.
            self._widest_line_width = max(self._widest_line_width, widest)
            self._expand_key = self._expand_cache_key()

            # Update the virtual size - the width may have changed after adding
            # the new line(s), and the height will definitely have changed.
            self.virtual_size = Size(self._widest_line_width, len(self.lines))
            apply_write_scroll(
                self,
                follow=follow,
                removed_lines=removed,
                old_scroll_y=old_scroll_y,
                animate=animate,
                immediate=not animate,
            )
            self.refresh()
        return self

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
        self._deferred_renders.clear()
        self.virtual_size = Size(0, len(self.lines))
        self.refresh()
        set_following_end(self, True)
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

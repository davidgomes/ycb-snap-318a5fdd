from __future__ import annotations

import re
from typing import TYPE_CHECKING, Iterable, Optional, Sequence

from rich.cells import cell_len
from rich.highlighter import Highlighter, ReprHighlighter
from rich.style import Style
from rich.text import Text

from textual import work
from textual._line_split import line_split
from textual.cache import LRUCache
from textual.geometry import Size
from textual.message import Message
from textual.reactive import var
from textual.scroll_view import ScrollView
from textual.selection import Selection
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

_sub_escape = re.compile("[\u0000-\u0014]").sub


class Log(ScrollView, can_focus=True):
    """A widget to log text."""

    ALLOW_SELECT = True
    DEFAULT_CSS = """
    Log {
        background: $surface;
        color: $text;
        overflow: scroll;
        &:focus {
            background-tint: $foreground 5%;
        }
    }
    """

    max_lines: var[int | None] = var[Optional[int]](None)
    """Maximum number of lines to show"""

    auto_scroll: var[bool] = var(True)
    """Scroll to the end on write when the log is already following the end."""

    _following_end = True
    _suspend_follow_sync = False

    class FollowChanged(Message):
        """Posted when the end-follow state changes.

        The message is posted only when
        [`is_following_end`][textual.widgets.Log.is_following_end] changes.

        Can be handled using `on_log_follow_changed` in a parent widget.
        """

        def __init__(
            self,
            widget: Log,
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
        def control(self) -> Log:
            """The log whose follow state changed.

            This is an alias for [`FollowChanged.widget`][textual.widgets.Log.FollowChanged.widget].
            """
            return self.widget

    def __init__(
        self,
        highlight: bool = False,
        max_lines: int | None = None,
        auto_scroll: bool = True,
        name: str | None = None,
        id: str | None = None,
        classes: str | None = None,
        disabled: bool = False,
    ) -> None:
        """Create a Log widget.

        Args:
            highlight: Enable highlighting.
            max_lines: Maximum number of lines to display.
            auto_scroll: Scroll to end on new lines.
            name: The name of the text log.
            id: The ID of the text log in the DOM.
            classes: The CSS classes of the text log.
            disabled: Whether the text log is disabled or not.
        """
        super().__init__(name=name, id=id, classes=classes, disabled=disabled)
        init_follow_state(self)
        self.highlight = highlight
        """Enable highlighting."""
        self.max_lines = max_lines
        self.auto_scroll = auto_scroll
        self._lines: list[str] = []
        self._width = 0
        self._updates = 0
        self._render_line_cache: LRUCache[int, Strip] = LRUCache(1024)
        self.highlighter: Highlighter = ReprHighlighter()
        """The Rich Highlighter object to use, if `highlight=True`"""
        self._clear_y = 0

    @property
    def allow_select(self) -> bool:
        return True

    @property
    def is_following_end(self) -> bool:
        """Whether new writes should keep the view pinned to the end of the log.

        This starts as `True`. It becomes `False` when the user scrolls away
        from the end, and `True` again when the view returns to the end or
        [`follow_end`][textual.widgets.Log.follow_end] is called.

        While [`auto_scroll`][textual.widgets.Log.auto_scroll] is enabled,
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

    def _scroll_end_if_following(self, animate: bool = False) -> None:
        """Scroll to the end if this log is still following it."""
        scroll_end_if_following(self, animate)

    @property
    def lines(self) -> Sequence[str]:
        """The raw lines in the Log.

        Note that this attribute is read only.
        Changing the lines will not update the Log's contents.

        """
        return self._lines

    def notify_style_update(self) -> None:
        """Called by Textual when styles update."""
        super().notify_style_update()
        self._render_line_cache.clear()

    def _update_maximum_width(self, updates: int, size: int) -> None:
        """Update the virtual size width.

        Args:
            updates: A counter of updates.
            size: Maximum size of new lines.
        """
        if updates == self._updates:
            self._width = max(size, self._width)
            self.virtual_size = Size(self._width, self.line_count)

    @property
    def line_count(self) -> int:
        """Number of lines of content."""
        if self._lines:
            return len(self._lines) - (self._lines[-1] == "")
        return 0

    @classmethod
    def _process_line(cls, line: str) -> str:
        """Process a line before it is rendered to remove control codes.

        Args:
            line: A string.

        Returns:
            New string with no control codes.
        """
        return _sub_escape("�", line.expandtabs())

    @work(thread=True)
    def _update_size(self, updates: int, lines: list[str]) -> None:
        """A thread worker to update the width in the background.

        Args:
            updates: The update index at the time of invocation.
            lines: Lines that were added.
        """
        if lines:
            _process_line = self._process_line
            max_length = max(cell_len(_process_line(line)) for line in lines)
            self.app.call_from_thread(self._update_maximum_width, updates, max_length)

    def _prune_max_lines(self) -> int:
        """Prune lines if there are more than the maximum.

        Returns:
            The number of lines removed from the start of the log.
        """
        if self.max_lines is None:
            return 0
        remove_lines = len(self._lines) - self.max_lines
        if remove_lines > 0:
            _cache = self._render_line_cache
            # We've removed some lines, which means the y values in the cache are out of sync
            # Calculated a new dict of cache values
            updated_cache = {
                y - remove_lines: _cache[y] for y in _cache.keys() if y > remove_lines
            }
            # Clear the cache
            _cache.clear()
            # Update the cache with previously calculated values
            for y, line in updated_cache.items():
                _cache[y] = line
            del self._lines[:remove_lines]
            return remove_lines
        return 0

    def write(
        self,
        data: str,
        scroll_end: bool | None = None,
    ) -> Self:
        """Write to the log.

        Args:
            data: Data to write.
            scroll_end: Scroll to the end after writing, or `None` to use
                `self.auto_scroll` while the log is following the end.

        Returns:
            The `Log` instance.
        """
        follow, old_scroll_y = capture_write_follow(self, scroll_end)
        with suspend_follow_sync(self):
            if data:
                if not self._lines:
                    self._lines.append("")
                for line, ending in line_split(data):
                    self._lines[-1] += line
                    self._width = max(
                        self._width, cell_len(self._process_line(self._lines[-1]))
                    )
                    self.refresh_lines(len(self._lines) - 1)
                    if ending:
                        self._lines.append("")

            removed = self._prune_max_lines()
            if data or removed:
                self.virtual_size = Size(self._width, self.line_count)
            apply_write_scroll(
                self,
                follow=follow,
                removed_lines=removed,
                old_scroll_y=old_scroll_y,
                animate=False,
                immediate=True,
            )
            if removed or not follow:
                self.refresh()
        return self

    def write_line(
        self,
        line: str,
        scroll_end: bool | None = None,
    ) -> Self:
        """Write content on a new line.

        Args:
            line: String to write to the log.
            scroll_end: Scroll to the end after writing, or `None` to use
                `self.auto_scroll` while the log is following the end.

        Returns:
            The `Log` instance.
        """
        self.write_lines([line], scroll_end)
        return self

    def write_lines(
        self,
        lines: Iterable[str],
        scroll_end: bool | None = None,
    ) -> Self:
        """Write an iterable of lines.

        Args:
            lines: An iterable of strings to write.
            scroll_end: Scroll to the end after writing, or `None` to use
                `self.auto_scroll` while the log is following the end.

        Returns:
            The `Log` instance.
        """
        follow, old_scroll_y = capture_write_follow(self, scroll_end)
        with suspend_follow_sync(self):
            new_lines = []
            for line in lines:
                new_lines.extend(line.splitlines())
            start_line = len(self._lines)
            self._lines.extend(new_lines)
            removed = self._prune_max_lines()
            if removed:
                start_line = max(0, start_line - removed)
            self.virtual_size = Size(self._width, len(self._lines))
            self._update_size(self._updates, new_lines)
            if removed:
                self.refresh()
            elif new_lines:
                self.refresh_lines(start_line, len(new_lines))
            apply_write_scroll(
                self,
                follow=follow,
                removed_lines=removed,
                old_scroll_y=old_scroll_y,
                animate=False,
                immediate=True,
            )
            if not follow:
                self.refresh()
        return self

    def clear(self) -> Self:
        """Clear the Log.

        Returns:
            The `Log` instance.
        """
        self._lines.clear()
        self._width = 0
        self._render_line_cache.clear()
        self._updates += 1
        self.virtual_size = Size(0, 0)
        self._clear_y = 0
        set_following_end(self, True)
        return self

    def get_selection(self, selection: Selection) -> tuple[str, str] | None:
        """Get the text under the selection.

        Args:
            selection: Selection information.

        Returns:
            Tuple of extracted text and ending (typically "\n" or " "), or `None` if no text could be extracted.
        """
        text = "\n".join(self._lines)
        return selection.extract(text), "\n"

    def selection_updated(self, selection: Selection | None) -> None:
        self._render_line_cache.clear()
        self.refresh()

    def render_line(self, y: int) -> Strip:
        """Render a line of content.

        Args:
            y: Y Coordinate of line.

        Returns:
            A rendered line.
        """
        scroll_x, scroll_y = self.scroll_offset
        strip = self._render_line(scroll_y + y, scroll_x, self.size.width)
        return strip

    def _render_line(self, y: int, scroll_x: int, width: int) -> Strip:
        """Render a line into a cropped strip.

        Args:
            y: Y offset of line.
            scroll_x: Current horizontal scroll.
            width: Width of the widget.

        Returns:
            A Strip suitable for rendering.
        """
        rich_style = self.rich_style
        if y >= len(self._lines):
            return Strip.blank(width, rich_style)

        line = self._render_line_strip(y, rich_style)
        assert line._cell_length is not None
        line = line.crop_extend(scroll_x, scroll_x + width, rich_style)
        line = line.apply_offsets(scroll_x, y)
        return line

    def _render_line_strip(self, y: int, rich_style: Style) -> Strip:
        """Render a line into a Strip.

        Args:
            y: Y offset of line.
            rich_style: Rich style of line.

        Returns:
            An uncropped Strip.
        """
        selection = self.text_selection
        if y in self._render_line_cache and selection is None:
            return self._render_line_cache[y]

        _line = self._process_line(self._lines[y])

        line_text = Text(_line, no_wrap=True)
        line_text.stylize(rich_style)

        if self.highlight:
            line_text = self.highlighter(line_text)
        if selection is not None:
            if (select_span := selection.get_span(y - self._clear_y)) is not None:
                start, end = select_span
                if end == -1:
                    end = len(line_text)

                selection_style = self.screen.get_component_rich_style(
                    "screen--selection"
                )
                line_text.stylize(selection_style, start, end)

        line = Strip(line_text.render(self.app.console), cell_len(_line))

        if selection is not None:
            self._render_line_cache[y] = line
        return line

    def refresh_lines(self, y_start: int, line_count: int = 1) -> None:
        """Refresh one or more lines.

        Args:
            y_start: First line to refresh.
            line_count: Total number of lines to refresh.
        """
        for y in range(y_start, y_start + line_count):
            self._render_line_cache.discard(y)
        super().refresh_lines(y_start, line_count=line_count)

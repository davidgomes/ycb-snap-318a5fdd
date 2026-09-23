"""Shared follow-the-end state for scrolling log widgets."""

from __future__ import annotations

from typing import TYPE_CHECKING

from textual.message import Message

if TYPE_CHECKING:
    from textual.scroll_view import ScrollView


class FollowChangedBase(Message):
    """Base for messages posted when a log starts or stops following its end."""

    def __init__(
        self,
        widget: ScrollView,
        is_following_end: bool,
        scroll_y: float,
        max_scroll_y: int,
    ) -> None:
        super().__init__()
        self.widget = widget
        """The log widget whose follow state changed."""
        self.is_following_end = is_following_end
        """True if the widget is now following the end."""
        self.scroll_y = scroll_y
        """The vertical scroll offset at the time of the change."""
        self.max_scroll_y = max_scroll_y
        """The maximum vertical scroll offset at the time of the change."""

    @property
    def control(self) -> ScrollView:
        return self.widget


class FollowEndMixin:
    """Tracks whether a scroll view is following the end of its content.

    Classes using this mixin must be `ScrollView` subclasses which define a
    `FollowChanged` message class derived from `FollowChangedBase`.
    """

    _following_end: bool = True
    _follow_scroll_pending: bool = False

    @property
    def is_following_end(self) -> bool:
        """Is the widget following (sticking to) the end of its content?"""
        return self._following_end

    def _set_following_end(self, following: bool) -> None:
        if following == self._following_end:
            return
        self._following_end = following
        widget: ScrollView = self  # type: ignore[assignment]
        widget.post_message(
            self.FollowChanged(  # type: ignore[attr-defined]
                widget, following, widget.scroll_y, widget.max_scroll_y
            )
        )

    def _follow_scroll_complete(self) -> None:
        self._follow_scroll_pending = False
        widget: ScrollView = self  # type: ignore[assignment]
        self._set_following_end(widget.is_vertical_scroll_end)

    def _scroll_to_end(self, animate: bool = False, immediate: bool = False) -> None:
        """Scroll to the end without dropping follow state mid-scroll."""
        widget: ScrollView = self  # type: ignore[assignment]
        self._follow_scroll_pending = True
        widget.scroll_end(
            animate=animate,
            immediate=immediate,
            x_axis=False,
            on_complete=self._follow_scroll_complete,
        )

    def follow_end(self, animate: bool = False) -> None:
        """Scroll to the end and resume following new content.

        Args:
            animate: Animate the scroll.
        """
        self._set_following_end(True)
        self._scroll_to_end(animate=animate)

    def _update_follow_from_scroll(self, new_value: float) -> None:
        widget: ScrollView = self  # type: ignore[assignment]
        at_end = round(new_value) >= widget.max_scroll_y
        if self._follow_scroll_pending:
            if at_end:
                self._follow_scroll_pending = False
            else:
                return
        self._set_following_end(at_end)

    def _keep_viewport(self, removed_lines: int) -> None:
        """Shift the scroll offset to compensate for lines removed from the top."""
        widget: ScrollView = self  # type: ignore[assignment]
        if removed_lines <= 0 or self._following_end:
            return
        new_y = max(0, widget.scroll_y - removed_lines)
        widget.scroll_target_y = new_y
        widget.scroll_y = new_y
        widget.refresh()

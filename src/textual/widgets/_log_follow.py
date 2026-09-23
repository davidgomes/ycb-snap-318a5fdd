"""Shared "follow the end" behavior for the `Log` and `RichLog` widgets."""

from __future__ import annotations

from typing import TYPE_CHECKING, ClassVar

from textual.message import Message

if TYPE_CHECKING:
    from textual.scroll_view import ScrollView

    _Base = ScrollView
else:
    _Base = object


class FollowChangedMessage(Message):
    """Base class for the `FollowChanged` messages of the log widgets."""

    def __init__(
        self,
        widget: ScrollView,
        is_following_end: bool,
        scroll_y: float,
        max_scroll_y: int,
    ) -> None:
        """
        Args:
            widget: The log widget whose follow state changed.
            is_following_end: `True` if the widget is now following the end.
            scroll_y: Vertical scroll offset at the time of the change.
            max_scroll_y: Maximum vertical scroll offset at the time of the change.
        """
        super().__init__()
        self.widget = widget
        """The log widget whose follow state changed."""
        self.is_following_end = is_following_end
        """`True` if the widget is now following the end."""
        self.scroll_y = scroll_y
        """Vertical scroll offset at the time of the change."""
        self.max_scroll_y = max_scroll_y
        """Maximum vertical scroll offset at the time of the change."""

    @property
    def control(self) -> ScrollView:
        """Alias for `widget`."""
        return self.widget


class FollowEndMixin(_Base):
    """Tracks whether a scrolling log is "following" (pinned to) its end."""

    FollowChanged: ClassVar[type[FollowChangedMessage]]
    auto_scroll: bool

    _following_end: bool = True
    _follow_animating: bool = False

    @property
    def is_following_end(self) -> bool:
        """Is the widget following the end, so that new content scrolls into view?"""
        return self._following_end

    def follow_end(self, animate: bool = False) -> None:
        """Scroll to the end and follow new content as it is written.

        Args:
            animate: Animate the scroll to the end.
        """
        if animate:
            self._set_following_end(True)
            self._scroll_to_end_if_following(animate=True, immediate=True)
        else:
            # Scroll first, so that `FollowChanged` reports the final position.
            self._follow_animating = False
            self.scroll_end(animate=False, immediate=True, x_axis=False)
            self._set_following_end(True)

    def _is_at_end(self) -> bool:
        """Is the vertical scroll position at (or beyond) the maximum?"""
        return not self.size or self.scroll_y >= self.max_scroll_y

    def _set_following_end(self, following: bool) -> None:
        """Update the follow state, posting `FollowChanged` if it changes."""
        if following == self._following_end:
            return
        self._following_end = following
        self.post_message(
            self.FollowChanged(self, following, self.scroll_y, self.max_scroll_y)
        )

    def _update_following_end(self) -> None:
        """Derive the follow state from the current scroll position."""
        self._follow_animating = False
        self._set_following_end(self._is_at_end())

    def _scroll_to_end_if_following(
        self, animate: bool = False, immediate: bool = False
    ) -> None:
        """Scroll to the end, provided the widget is still following at scroll time.

        Args:
            animate: Animate the scroll.
            immediate: Scroll now, rather than after the next refresh.
        """

        def on_complete() -> None:
            self._follow_animating = False

        def scroll_end() -> None:
            if not self._following_end:
                return
            if animate:
                self._follow_animating = True
            self.scroll_end(
                animate=animate,
                immediate=True,
                x_axis=False,
                on_complete=on_complete,
            )

        if immediate:
            scroll_end()
        else:
            self.call_after_refresh(scroll_end)

    def _after_write(
        self,
        scroll_end: bool | None,
        removed_lines: int,
        animate: bool = False,
        immediate: bool = True,
    ) -> None:
        """Follow the end, or keep the viewport stable, after content was written.

        Args:
            scroll_end: `True` to scroll to the end, `False` to not scroll, or `None`
                to scroll only if `auto_scroll` is enabled and the log is following.
            removed_lines: Number of lines pruned from the start of the log.
            animate: Animate the scroll to the end.
            immediate: Scroll now, rather than after the next refresh.
        """
        if scroll_end is None:
            scroll_end = (
                self.auto_scroll
                and self._following_end
                and not self.is_vertical_scrollbar_grabbed
            )
        if scroll_end:
            if immediate:
                self.follow_end(animate=animate)
            else:
                self._set_following_end(True)
                self._scroll_to_end_if_following(animate=animate)
            return
        self._preserve_viewport(removed_lines)
        if self.size:
            self._update_following_end()
        self.refresh()

    def _preserve_viewport(self, removed_lines: int) -> None:
        """Keep the viewport on the same content after lines were removed from the top.

        Args:
            removed_lines: Number of lines removed from the start of the log.
        """
        if removed_lines <= 0 or self._following_end:
            return
        scroll_y = max(0, round(self.scroll_y) - removed_lines)
        self.scroll_target_y = scroll_y
        self.scroll_y = scroll_y

    def watch_scroll_y(self, old_value: float, new_value: float) -> None:
        super().watch_scroll_y(old_value, new_value)
        if self._follow_animating and new_value >= old_value:
            if new_value >= self.max_scroll_y:
                self._follow_animating = False
            return
        self._update_following_end()

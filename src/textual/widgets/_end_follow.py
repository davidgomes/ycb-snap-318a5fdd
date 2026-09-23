"""Shared follow-end scrolling for line-oriented log widgets."""

from __future__ import annotations

from textual.message import Message
from textual.scroll_view import ScrollView


class _EndFollow(ScrollView):
    """Track whether a scrollable log is anchored to the end.

    Subclasses must define a nested ``FollowChanged`` message whose constructor
    accepts ``(widget, is_following_end, scroll_y, max_scroll_y)``.
    """

    auto_scroll: bool
    _following_end: bool = True
    _suppress_follow_sync: int = 0
    _hold_follow: bool = False

    class FollowChanged(Message):
        """Base follow-end message.

        ``Log`` and ``RichLog`` replace this so the handler name matches the
        widget that posted it.
        """

        def __init__(
            self,
            widget: _EndFollow,
            is_following_end: bool,
            scroll_y: float,
            max_scroll_y: int,
        ) -> None:
            super().__init__()
            self.widget = widget
            self.is_following_end = is_following_end
            self.scroll_y = scroll_y
            self.max_scroll_y = max_scroll_y

    @property
    def is_following_end(self) -> bool:
        """Whether new writes should keep this widget scrolled to the end.

        This becomes ``False`` when the user scrolls away from the end, and
        ``True`` again when the viewport returns to the end or ``follow_end``
        is called.
        """
        return self._following_end

    def follow_end(self, animate: bool = False) -> None:
        """Scroll to the end and resume following new writes.

        Args:
            animate: Animate the scroll when ``True``.
        """
        self._scroll_end_and_follow(animate=animate, immediate=not animate)

    def _scroll_end_and_follow(self, *, animate: bool, immediate: bool) -> None:
        """Scroll to the end and record that the widget is following."""
        if animate:
            self._hold_follow = True
            self.scroll_end(animate=True, immediate=immediate, x_axis=False)
            if self.is_vertical_scroll_end:
                self._hold_follow = False
            self._set_following_end(True)
            return

        self._suppress_follow_sync += 1
        try:
            self.scroll_end(animate=False, immediate=immediate, x_axis=False)
        finally:
            self._suppress_follow_sync -= 1
        self._hold_follow = False
        self._set_following_end(True)

    def _set_following_end(self, following: bool) -> None:
        """Update follow state and post ``FollowChanged`` when it changes."""
        following = bool(following)
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

    def _preserve_scroll_after_prune(
        self, previous_scroll_y: float, removed_lines: int
    ) -> None:
        """Keep the same lines on screen after lines are dropped from the top."""
        if removed_lines <= 0:
            return
        target = max(0.0, previous_scroll_y - removed_lines)
        self._suppress_follow_sync += 1
        try:
            self.scroll_to(y=target, animate=False, immediate=True)
        finally:
            self._suppress_follow_sync -= 1

    def watch_scroll_y(self, old_value: float, new_value: float) -> None:
        super().watch_scroll_y(old_value, new_value)
        if self._suppress_follow_sync or not self.is_mounted:
            return
        if self._hold_follow:
            if self.is_vertical_scroll_end:
                self._hold_follow = False
                self._set_following_end(True)
                return
            # A scroll away from the end cancels an in-progress animated follow.
            if self.scroll_target_y < self.max_scroll_y:
                self._hold_follow = False
                self._set_following_end(False)
            return
        self._set_following_end(self.is_vertical_scroll_end)

    def _follow_after_write(
        self,
        *,
        was_following: bool,
        scroll_end: bool | None,
        previous_scroll_y: float,
        removed_lines: int,
        animate: bool = False,
        immediate: bool | None = None,
    ) -> None:
        """Apply follow or viewport-stability rules after lines are appended."""
        if scroll_end is None:
            should_follow = bool(self.auto_scroll) and was_following
        else:
            should_follow = bool(scroll_end)
        if self.is_vertical_scrollbar_grabbed:
            should_follow = False

        if should_follow:
            if immediate is None:
                immediate = not animate
            self._scroll_end_and_follow(animate=animate, immediate=immediate)
            return

        self._preserve_scroll_after_prune(previous_scroll_y, removed_lines)
        self._set_following_end(self.is_vertical_scroll_end)

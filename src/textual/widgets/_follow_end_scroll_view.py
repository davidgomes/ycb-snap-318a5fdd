"""Provides the common follow behavior of `Log` and `RichLog`."""

from __future__ import annotations

from textual._animator import EasingFunction
from textual._types import AnimationLevel, CallbackType
from textual.events import Resize
from textual.message import Message
from textual.reactive import var
from textual.scroll_view import ScrollView


class FollowEndScrollView(ScrollView):
    """A scroll view which may follow the end of its content as it grows.

    Warning:
        `FollowEndScrollView` should be considered to be an internal class; it
        exists to serve as the common core of [Log][textual.widgets.Log] and
        [RichLog][textual.widgets.RichLog].
    """

    class FollowChanged(Message):
        """Posted when the widget starts or stops following the end of its content."""

        def __init__(
            self,
            widget: FollowEndScrollView,
            is_following_end: bool,
            scroll_y: float,
            max_scroll_y: int,
        ) -> None:
            """Initialise the message.

            Args:
                widget: The widget which started or stopped following the end.
                is_following_end: The new follow state.
                scroll_y: The vertical scroll position when the state changed.
                max_scroll_y: The maximum vertical scroll position when the state changed.
            """
            super().__init__()
            self.widget = widget
            """The widget which started or stopped following the end."""
            self.is_following_end = is_following_end
            """`True` if the widget is now following the end, `False` if it stopped."""
            self.scroll_y = scroll_y
            """The vertical scroll position when the state changed."""
            self.max_scroll_y = max_scroll_y
            """The maximum vertical scroll position when the state changed."""

        @property
        def control(self) -> FollowEndScrollView:
            """An alias for `widget`, used by the [`on`][textual.on] decorator."""
            return self.widget

    auto_scroll: var[bool] = var(True)
    """Automatically scroll to new content while following the end."""

    _following_end: bool = True

    @property
    def is_following_end(self) -> bool:
        """Is the widget following the end of its content?

        While following, writes scroll new content into view (if `auto_scroll` is enabled).
        Scrolling away from the end stops following, and scrolling back to the end resumes it.
        """
        return self._following_end

    def follow_end(self, animate: bool = False) -> None:
        """Scroll to the end of the content, and follow it as new content is written.

        Args:
            animate: Animate the scroll to the end.
        """
        self._set_following_end(True)
        self.scroll_end(animate=animate, immediate=True, x_axis=False)

    def _set_following_end(self, following: bool) -> None:
        """Update the follow state, posting `FollowChanged` if it changed.

        Args:
            following: The new follow state.
        """
        if following != self._following_end:
            self._following_end = following
            self.post_message(
                self.FollowChanged(self, following, self.scroll_y, self.max_scroll_y)
            )

    def _is_scroll_end(self, scroll_y: float) -> bool:
        """Check if a vertical scroll position shows the end of the content.

        Args:
            scroll_y: A vertical scroll position.

        Returns:
            `True` if the position is at the end.
        """
        return round(scroll_y) >= self.max_scroll_y

    def _scroll_to(
        self,
        x: float | None = None,
        y: float | None = None,
        *,
        animate: bool = True,
        speed: float | None = None,
        duration: float | None = None,
        easing: EasingFunction | str | None = None,
        force: bool = False,
        on_complete: CallbackType | None = None,
        level: AnimationLevel = "basic",
        release_anchor: bool = True,
    ) -> bool:
        # The follow state is decided from the requested position, rather than
        # waiting for an animation to move `scroll_y`, so a write made while the
        # scroll is in flight can't snap the widget back to the end.
        following: bool | None = None
        if y is not None and (self.allow_vertical_scroll or force):
            target_y = self.validate_scroll_target_y(y)
            if self._is_scroll_end(target_y):
                following = True
            elif target_y < max(self.scroll_y, self.scroll_target_y):
                following = False
        scrolled = super()._scroll_to(
            x,
            y,
            animate=animate,
            speed=speed,
            duration=duration,
            easing=easing,
            force=force,
            on_complete=on_complete,
            level=level,
            release_anchor=release_anchor,
        )
        if following is not None:
            self._set_following_end(following)
        return scrolled

    def _watch_scroll_y(self, old_value: float, new_value: float) -> None:
        if self._is_scroll_end(new_value):
            self._set_following_end(True)
        elif new_value < old_value:
            self._set_following_end(False)

    def _on_resize(self, event: Resize) -> None:
        if self.auto_scroll and self._following_end:
            self._scroll_end_after_refresh()

    def _should_follow_write(self, scroll_end: bool | None) -> bool:
        """Check if a write should scroll to the end.

        Args:
            scroll_end: The `scroll_end` argument of the write.

        Returns:
            `True` if the write should scroll to the end.
        """
        if self.is_vertical_scrollbar_grabbed:
            return False
        if scroll_end is None:
            return self.auto_scroll and self._following_end
        return scroll_end

    def _scroll_after_write(
        self,
        follow: bool,
        removed_lines: int,
        *,
        immediate: bool,
        animate: bool = False,
    ) -> None:
        """Update the scroll position after content was written.

        Args:
            follow: Scroll to the end, as returned by `_should_follow_write`.
            removed_lines: Number of lines removed from the start of the content.
            immediate: Scroll immediately, rather than after a refresh.
            animate: Animate the scroll to the end.
        """
        if follow:
            self._set_following_end(True)
            if immediate:
                self.scroll_end(animate=animate, immediate=True, x_axis=False)
            else:
                self._scroll_end_after_refresh(animate)
            return
        if removed_lines and self.scroll_y:
            # Keep the same content in the viewport when lines are removed above it.
            self.app.animator.force_stop_animation(self, "scroll_y")
            scroll_y = max(0.0, self.scroll_y - removed_lines)
            self.scroll_target_y = scroll_y
            self.scroll_y = scroll_y
        if self._following_end and not self.is_vertical_scrollbar_grabbed:
            self.call_after_refresh(self._sync_following_end)

    def _scroll_end_after_refresh(self, animate: bool = False) -> None:
        """Scroll to the end after the next refresh, if still following.

        Args:
            animate: Animate the scroll.
        """
        self.call_after_refresh(self._scroll_end_if_following, animate)

    def _scroll_end_if_following(self, animate: bool = False) -> None:
        """Scroll to the end if following.

        Args:
            animate: Animate the scroll.
        """
        if self._following_end and not self.is_vertical_scrollbar_grabbed:
            self.scroll_end(animate=animate, immediate=True, x_axis=False)

    def _sync_following_end(self) -> None:
        """Update the follow state from the scroll position."""
        self._set_following_end(self._is_scroll_end(self.scroll_y))

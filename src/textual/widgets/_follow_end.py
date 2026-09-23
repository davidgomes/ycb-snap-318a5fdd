"""Shared end-follow behavior for [`Log`][textual.widgets.Log] and [`RichLog`][textual.widgets.RichLog]."""

from __future__ import annotations

from collections.abc import Iterator
from contextlib import contextmanager
from typing import Any


def init_follow_state(widget: Any) -> None:
    """Set the initial end-follow state on a log widget.

    Args:
        widget: Log or RichLog instance.
    """
    widget._following_end = True
    widget._suspend_follow_sync = False


def set_following_end(widget: Any, following: bool) -> None:
    """Update end-follow state and post `FollowChanged` when it changes.

    Args:
        widget: Log or RichLog instance.
        following: New follow state.
    """
    following = bool(following)
    if widget._following_end == following:
        return
    widget._following_end = following
    widget.post_message(
        widget.FollowChanged(
            widget,
            following,
            widget.scroll_y,
            widget.max_scroll_y,
        )
    )


def sync_follow_on_scroll(widget: Any, old_value: float, new_value: float) -> None:
    """Update follow state after a vertical scroll.

    Arriving at the end restores follow. Moving upward away from the end
    releases it. Downward motion that has not reached the end leaves the
    current mode unchanged so an in-progress ``follow_end`` animation is
    not cancelled.

    Args:
        widget: Log or RichLog instance.
        old_value: Previous `scroll_y`.
        new_value: New `scroll_y`.
    """
    if widget._suspend_follow_sync:
        return
    if widget.is_vertical_scroll_end:
        set_following_end(widget, True)
    elif new_value < old_value:
        set_following_end(widget, False)


def capture_write_follow(widget: Any, scroll_end: bool | None) -> tuple[bool, float]:
    """Decide whether a write should scroll, before the content changes.

    Args:
        widget: Log or RichLog instance.
        scroll_end: Write argument. `None` uses `auto_scroll` and the
            current follow state.

    Returns:
        A pair of ``(should_follow, scroll_y)`` captured before the write.
    """
    if widget.is_vertical_scrollbar_grabbed:
        follow = False
    elif scroll_end is None:
        follow = bool(widget.auto_scroll and widget._following_end)
    else:
        follow = bool(scroll_end)
    return follow, float(widget.scroll_y)


@contextmanager
def suspend_follow_sync(widget: Any) -> Iterator[None]:
    """Ignore scroll-driven follow updates while content is being mutated.

    Args:
        widget: Log or RichLog instance.
    """
    previous = widget._suspend_follow_sync
    widget._suspend_follow_sync = True
    try:
        yield
    finally:
        widget._suspend_follow_sync = previous


def apply_write_scroll(
    widget: Any,
    *,
    follow: bool,
    removed_lines: int,
    old_scroll_y: float,
    animate: bool = False,
    immediate: bool = True,
) -> None:
    """Scroll after a write, or keep the current lines on screen.

    When ``follow`` is true, scroll to the end. Otherwise shift `scroll_y`
    up by the number of lines pruned from the top so the viewport stays on
    the same content.

    Args:
        widget: Log or RichLog instance.
        follow: Whether this write should pin the view to the end.
        removed_lines: Lines removed from the top by `max_lines` pruning.
        old_scroll_y: `scroll_y` from before the content changed.
        animate: Animate a scroll to the end.
        immediate: Scroll to the end immediately instead of after refresh.
    """
    if follow:
        set_following_end(widget, True)
        if immediate:
            widget.scroll_end(animate=animate, immediate=True, x_axis=False)
        else:
            widget.call_after_refresh(widget._scroll_end_if_following, animate)
        return

    target = max(0.0, old_scroll_y - removed_lines)
    if target != widget.scroll_y or target != widget.scroll_target_y:
        widget.scroll_to(y=target, animate=False, immediate=True)
    # A write that does not pin to the end leaves follow on only while the
    # viewport is still sitting on the end (for example when the new lines
    # still fit). Growing past the viewport releases follow.
    set_following_end(widget, widget.is_vertical_scroll_end)


def scroll_end_if_following(widget: Any, animate: bool = False) -> None:
    """Scroll to the end if the widget is still following it.

    Args:
        widget: Log or RichLog instance.
        animate: Animate the scroll.
    """
    if not widget._following_end or widget.is_vertical_scrollbar_grabbed:
        return
    with suspend_follow_sync(widget):
        widget.scroll_end(animate=animate, immediate=True, x_axis=False)


def request_follow_end(widget: Any, *, animate: bool) -> None:
    """Scroll to the end and mark the widget as following.

    Args:
        widget: Log or RichLog instance.
        animate: Animate the scroll.
    """
    if animate:
        set_following_end(widget, True)
        widget.scroll_end(animate=True, immediate=False, x_axis=False)
        return
    widget.scroll_end(animate=False, immediate=True, x_axis=False)
    set_following_end(widget, True)

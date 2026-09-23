"""Shared follow-end behavior for scrolling log widgets."""

from __future__ import annotations

from typing import Any


def set_following_end(widget: Any, following: bool) -> None:
    """Update follow state and post `FollowChanged` only when it changes."""
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


def sync_following_end(widget: Any) -> None:
    """Restore follow at the end, and stop following when the user scrolls away."""
    if widget._suspend_follow_sync:
        return
    if widget.is_vertical_scroll_end:
        set_following_end(widget, True)
    elif widget.scroll_target_y < widget.max_scroll_y:
        set_following_end(widget, False)


def wants_end_scroll(widget: Any, scroll_end: bool | None) -> bool:
    """Whether a write should scroll to the end.

    With `auto_scroll`, follow only while the widget is already following the end.
    An explicit `scroll_end` value overrides that.
    """
    if widget.is_vertical_scrollbar_grabbed:
        return False
    if scroll_end is None:
        return bool(widget.auto_scroll and widget.is_following_end)
    return bool(scroll_end)


def shift_scroll_y(widget: Any, delta: int) -> None:
    """Move the viewport by `delta` lines without clearing follow state.

    Landing on the end restores follow.
    """
    if not delta:
        return
    widget._suspend_follow_sync = True
    try:
        widget.scroll_to(
            y=widget.scroll_y + delta,
            animate=False,
            immediate=True,
        )
    finally:
        widget._suspend_follow_sync = False
    if widget.is_vertical_scroll_end:
        set_following_end(widget, True)

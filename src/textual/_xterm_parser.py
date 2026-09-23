from __future__ import annotations

import os
import re
from typing import Any, Generator, Iterable

from typing_extensions import Final

from textual import constants, events, messages
from textual._ansi_sequences import ANSI_SEQUENCES_KEYS, IGNORE_SEQUENCE
from textual._keyboard_protocol import FUNCTIONAL_KEYS
from textual._parser import ParseEOF, Parser, ParseTimeout, Peek1, Read1, TokenCallback
from textual.keys import (
    KEY_NAME_REPLACEMENTS,
    Keys,
    _character_to_key,
    _split_key_modifiers,
)
from textual.message import Message

# When trying to determine whether the current sequence is a supported/valid
# escape sequence, at which length should we give up and consider our search
# to be unsuccessful?
_MAX_SEQUENCE_SEARCH_THRESHOLD = 32

_re_mouse_event = re.compile("^" + re.escape("\x1b[") + r"(<?[-\d;]+[mM]|M...)\Z")
_re_terminal_mode_response = re.compile(
    "^" + re.escape("\x1b[") + r"\?(?P<mode_id>\d+);(?P<setting_parameter>\d)\$y"
)

_re_cursor_position = re.compile(r"\x1b\[(?P<row>\d+);(?P<col>\d+)R")

BRACKETED_PASTE_START: Final[str] = "\x1b[200~"
"""Sequence received when a bracketed paste event starts."""
BRACKETED_PASTE_END: Final[str] = "\x1b[201~"
"""Sequence received when a bracketed paste event ends."""
FOCUSIN: Final[str] = "\x1b[I"
"""Sequence received when the terminal receives focus."""
FOCUSOUT: Final[str] = "\x1b[O"
"""Sequence received when focus is lost from the terminal."""

SPECIAL_SEQUENCES = {BRACKETED_PASTE_START, BRACKETED_PASTE_END, FOCUSIN, FOCUSOUT}
"""Set of special sequences."""

_re_extended_key: Final = re.compile(
    r"\x1b\[(?:([\d:]+)(?:;([\d:]*))?(?:;([\d:]*))?)?([u~ABCDEFHPQRS])"
)

_KITTY_MODIFIERS: Final = ("shift", "alt", "ctrl", "super", "hyper", "meta")
"""Kitty modifier bits, in bit order (caps_lock and num_lock are ignored)."""
_KITTY_PHASES: Final[dict[str, events.KeyPhase]] = {
    "1": "press",
    "2": "repeat",
    "3": "release",
}
_re_in_band_window_resize: Final = re.compile(
    r"\x1b\[48;(\d+(?:\:.*?)?);(\d+(?:\:.*?)?);(\d+(?:\:.*?)?);(\d+(?:\:.*?)?)t"
)


IS_ITERM = (
    os.environ.get("LC_TERMINAL", "") == "iTerm2"
    or os.environ.get("TERM_PROGRAM", "") == "iTerm.app"
)


class XTermParser(Parser[Message]):
    _re_sgr_mouse = re.compile(r"\x1b\[<(\d+);(-?\d+);(-?\d+)([Mm])")

    def __init__(self, debug: bool = False) -> None:
        self.last_x = 0.0
        self.last_y = 0.0
        self.mouse_pixels = False
        self.terminal_size: tuple[int, int] | None = None
        self.terminal_pixel_size: tuple[int, int] | None = None
        self._debug_log_file = open("keys.log", "at") if debug else None
        super().__init__()
        self.debug_log("---")

    def debug_log(self, *args: Any) -> None:  # pragma: no cover
        if self._debug_log_file is not None:
            self._debug_log_file.write(" ".join(args) + "\n")
            self._debug_log_file.flush()

    def feed(self, data: str) -> Iterable[Message]:
        self.debug_log(f"FEED {data!r}")
        return super().feed(data)

    def parse_mouse_code(self, code: str) -> Message | None:
        sgr_match = self._re_sgr_mouse.match(code)
        if sgr_match:
            _buttons, _x, _y, state = sgr_match.groups()
            buttons = int(_buttons)
            x = float(int(_x) - 1)
            y = float(int(_y) - 1)
            if x < 0 or y < 0:
                # TODO: Workaround for Ghostty erroneous negative coordinate bug
                return None
            if (
                self.mouse_pixels
                and self.terminal_pixel_size is not None
                and self.terminal_size is not None
            ):
                pixel_width, pixel_height = self.terminal_pixel_size
                width, height = self.terminal_size
                x_ratio = pixel_width / width
                y_ratio = pixel_height / height
                x /= x_ratio
                y /= y_ratio

            delta_x = int(x) - int(self.last_x)
            delta_y = int(y) - int(self.last_y)
            self.last_x = x
            self.last_y = y
            event_class: type[events.MouseEvent]

            if buttons & 64:
                event_class = [
                    events.MouseScrollUp,
                    events.MouseScrollDown,
                    events.MouseScrollLeft,
                    events.MouseScrollRight,
                ][buttons & 3]
                button = 0
            else:
                button = (buttons + 1) & 3
                # XTerm events for mouse movement can look like mouse button down events. But if there is no key pressed,
                # it's a mouse move event.
                if buttons & 32 or button == 0:
                    event_class = events.MouseMove
                else:
                    event_class = events.MouseDown if state == "M" else events.MouseUp

            event = event_class(
                None,
                x,
                y,
                delta_x,
                delta_y,
                button,
                bool(buttons & 4),
                bool(buttons & 8),
                bool(buttons & 16),
                screen_x=x,
                screen_y=y,
            )
            return event
        return None

    def parse(
        self, token_callback: TokenCallback
    ) -> Generator[Read1 | Peek1, str, None]:
        ESC = "\x1b"
        read1 = self.read1
        sequence_to_key_events = self._sequence_to_key_events
        paste_buffer: list[str] = []
        bracketed_paste = False

        def on_token(token: Message) -> None:
            """Hook to log events."""
            self.debug_log(str(token))
            if isinstance(token, events.Resize):
                self.terminal_size = token.size
                self.terminal_pixel_size = token.pixel_size
            token_callback(token)

        def on_key_token(event: events.Key) -> None:
            """Token callback wrapper for handling keys.

            Args:
                event: The key event to send to the callback.

            This wrapper looks for keys that should be ignored, and filters
            them out, logging the ignored sequence when it does.
            """
            if event.key == Keys.Ignore:
                self.debug_log(f"ignored={event.character!r}")
            else:
                on_token(event)

        def reissue_sequence_as_keys(
            reissue_sequence: str, process_alt: bool = False
        ) -> None:
            """Called when an escape sequence hasn't been understood.

            Args:
                reissue_sequence: Key sequence to report to the app.
            """

            alt = False

            if reissue_sequence:
                self.debug_log("REISSUE", repr(reissue_sequence))
                for character in reissue_sequence:
                    if process_alt and character == ESC:
                        alt = True
                        continue
                    key_events = sequence_to_key_events(character, alt=alt)
                    for event in key_events:
                        if event.key == "escape" and not process_alt:
                            event = events.Key("circumflex_accent", "^")
                        on_token(event)
                    alt = False

        while not self.is_eof:
            if not bracketed_paste and paste_buffer:
                # We're at the end of the bracketed paste.
                # The paste buffer has content, but the bracketed paste has finished,
                # so we flush the paste buffer. We have to remove the final character
                # since if bracketed paste has come to an end, we'll have added the
                # ESC from the closing bracket, since at that point we didn't know what
                # the full escape code was.
                pasted_text = "".join(paste_buffer[:-1])
                # Note the removal of NUL characters: https://github.com/Textualize/textual/issues/1661
                on_token(events.Paste(pasted_text.replace("\x00", "")))
                paste_buffer.clear()

            try:
                character = yield read1()
            except ParseEOF:
                return

            if bracketed_paste:
                paste_buffer.append(character)

            self.debug_log(f"character={character!r}")
            if character != ESC:
                if not bracketed_paste:
                    for event in sequence_to_key_events(character):
                        on_key_token(event)
                if not character:
                    return
                continue

            # # Could be the escape key was pressed OR the start of an escape sequence
            sequence: str = ESC

            def send_sequence(process_alt: bool = True) -> None:
                """Send escape key and reissue sequence."""
                if sequence == ESC:
                    on_token(events.Key("escape", "\x1b"))
                else:
                    reissue_sequence_as_keys(sequence, process_alt=process_alt)

            while True:
                try:
                    new_character = yield read1(constants.ESCAPE_DELAY)
                except ParseTimeout:
                    send_sequence()
                    break
                except ParseEOF:
                    send_sequence()
                    return

                if new_character == ESC:
                    send_sequence(process_alt=False)
                    sequence = character
                    continue
                else:
                    sequence += new_character
                    if len(sequence) > _MAX_SEQUENCE_SEARCH_THRESHOLD:
                        reissue_sequence_as_keys(sequence)
                        break

                self.debug_log(f"sequence={sequence!r}")
                if sequence in SPECIAL_SEQUENCES:
                    if sequence == FOCUSIN:
                        on_token(events.AppFocus())
                    elif sequence == FOCUSOUT:
                        on_token(events.AppBlur())
                    elif sequence == BRACKETED_PASTE_START:
                        bracketed_paste = True
                    elif sequence == BRACKETED_PASTE_END:
                        bracketed_paste = False
                    break
                if match := _re_in_band_window_resize.fullmatch(sequence):
                    height, width, pixel_height, pixel_width = [
                        group.partition(":")[0] for group in match.groups()
                    ]
                    resize_event = events.Resize.from_dimensions(
                        (int(width), int(height)),
                        (int(pixel_width), int(pixel_height)),
                    )

                    self.terminal_size = resize_event.size
                    self.terminal_pixel_size = resize_event.pixel_size
                    self.mouse_pixels = True
                    on_token(resize_event)
                    break

                if not bracketed_paste:
                    # Check cursor position report
                    cursor_position_match = _re_cursor_position.match(sequence)
                    if cursor_position_match is not None:
                        row, column = map(int, cursor_position_match.groups())
                        x = int(column) - 1
                        y = int(row) - 1
                        on_token(events.CursorPosition(x, y))
                        break

                    # Was it a pressed key event that we received?
                    key_events = list(sequence_to_key_events(sequence))
                    for key_event in key_events:
                        on_key_token(key_event)
                    if key_events:
                        break
                    # Or a mouse event?
                    mouse_match = _re_mouse_event.match(sequence)
                    if mouse_match is not None:
                        mouse_code = mouse_match.group(0)
                        mouse_event = self.parse_mouse_code(mouse_code)
                        if mouse_event is not None:
                            on_token(mouse_event)
                        break

                    # Or a mode report?
                    # (i.e. the terminal saying it supports a mode we requested)
                    mode_report_match = _re_terminal_mode_response.match(sequence)
                    if mode_report_match is not None:
                        mode_id = mode_report_match["mode_id"]
                        setting_parameter = int(mode_report_match["setting_parameter"])
                        if mode_id == "2026" and setting_parameter > 0:
                            on_token(messages.TerminalSupportsSynchronizedOutput())
                        elif (
                            mode_id == "2048"
                            and constants.SMOOTH_SCROLL
                            and not IS_ITERM
                        ):
                            # TODO: iTerm is buggy in one or more of the protocols required here
                            in_band_event = (
                                messages.InBandWindowResize.from_setting_parameter(
                                    setting_parameter
                                )
                            )
                            on_token(in_band_event)
                        break

        if self._debug_log_file is not None:
            self._debug_log_file.close()
            self._debug_log_file = None

    @classmethod
    def _parse_extended_key(cls, match: re.Match[str]) -> events.Key | None:
        """Build a key event from a Kitty keyboard protocol sequence.

        See https://sw.kovidgoyal.net/kitty/keyboard-protocol/

        Args:
            match: A match from `_re_extended_key`.

        Returns:
            A key event, or `None` if the sequence isn't a valid key.
        """
        key_codes, modifier_field, text_field, end = match.groups()
        key_codes = key_codes or "1"
        if end != "u" and (":" in key_codes or text_field is not None):
            # Alternate keys and associated text are only valid for CSI u
            return None

        code_str, shifted_str, base_layout_str, *_ = key_codes.split(":") + ["", ""]
        modifier_str, _, phase_str = (modifier_field or "").partition(":")
        if phase_str and phase_str not in _KITTY_PHASES:
            return None
        phase = _KITTY_PHASES.get(phase_str, "press")

        modifier_bits = max(0, int(modifier_str) - 1) if modifier_str else 0
        modifiers = sorted(
            modifier
            for bit, modifier in enumerate(_KITTY_MODIFIERS)
            if modifier_bits & (1 << bit)
        )
        non_shift_modifiers = [
            modifier for modifier in modifiers if modifier != "shift"
        ]
        shift = "shift" in modifiers

        def codepoint_to_character(codepoint: str) -> str | None:
            """Convert a decimal codepoint to a character (or `None`)."""
            if not codepoint or not codepoint.isdecimal():
                return None
            try:
                return chr(int(codepoint)) if int(codepoint) else None
            except (ValueError, OverflowError):
                return None

        def character_to_key(character: str) -> str:
            """Convert a character to a Textual key name."""
            try:
                return _character_to_key(character)
            except Exception:
                return character

        text = "".join(
            character
            for character in map(codepoint_to_character, (text_field or "").split(":"))
            if character is not None
        )
        if not text.isprintable():
            text = ""

        shifted_character = codepoint_to_character(shifted_str)
        shifted_key = (
            None if shifted_character is None else character_to_key(shifted_character)
        )
        base_layout_character = codepoint_to_character(base_layout_str)
        base_layout_key = (
            None
            if base_layout_character is None
            else character_to_key(base_layout_character)
        )

        def make_key(
            key: str, character: str | None, base_key: str, *extra_aliases: str
        ) -> events.Key:
            key_event = events.Key(
                key,
                character,
                phase=phase,
                modifiers=modifiers,
                base_key=base_key,
                shifted_key=shifted_key,
                base_layout_key=base_layout_key,
            )
            for alias in extra_aliases:
                if alias not in key_event.aliases:
                    key_event.aliases.append(alias)
            return key_event

        functional_key = FUNCTIONAL_KEYS.get(f"{code_str or 1}{end}")
        if functional_key is not None:
            return make_key(
                "+".join([*modifiers, functional_key]), None, functional_key
            )

        code_character = codepoint_to_character(code_str)
        if code_character is None:
            if not text:
                return None
            # Key code 0: the terminal only reported associated text.
            text_key = character_to_key(text) if len(text) == 1 else text
            if non_shift_modifiers:
                return make_key("+".join([*modifiers, text_key]), None, text_key)
            return make_key(text, text, text_key, text_key)

        lower_character = code_character.lower()
        base_character = (
            lower_character if len(lower_character) == 1 else code_character
        )
        base_key = character_to_key(base_character).lower()

        if non_shift_modifiers:
            return make_key("+".join([*modifiers, base_key]), None, base_key)

        character: str | None
        if shift:
            upper_character = base_character.upper()
            character = (
                text
                or shifted_character
                or (
                    upper_character
                    if len(upper_character) == 1 and upper_character != base_character
                    else None
                )
            )
            if character is None and base_character.isspace():
                character = base_character
        else:
            character = text or base_character
        if character is not None and not character.isprintable():
            character = None

        if character is None or (shift and character.isspace()):
            key = "+".join([*modifiers, base_key])
        elif len(character) == 1:
            key = character_to_key(character)
        else:
            key = character
        return make_key(key, character, base_key)

    def _sequence_to_key_events(
        self, sequence: str, alt: bool = False
    ) -> Iterable[events.Key]:
        """Map a sequence of code points on to a sequence of keys.

        Args:
            sequence: Sequence of code points.

        Returns:
            Keys
        """

        if (match := _re_extended_key.fullmatch(sequence)) is not None:
            if (key_event := self._parse_extended_key(match)) is not None:
                yield key_event
            return

        keys = ANSI_SEQUENCES_KEYS.get(sequence)
        # If we're being asked to ignore the key...
        if keys is IGNORE_SEQUENCE:
            # ...build a special ignore key event, which has the ignore
            # name as the key (that is, the key this sequence is bound
            # to is the ignore key) and the sequence that was ignored as
            # the character.
            yield events.Key(Keys.Ignore, sequence)
            return
        if isinstance(keys, tuple):
            # If the sequence mapped to a tuple, then it's values from the
            # `Keys` enum. Raise key events from what we find in the tuple.
            for key in keys:
                if alt and len(sequence) == 1:
                    # Legacy ESC prefixed key: add alt to the existing key name.
                    key_modifiers, base_key = _split_key_modifiers(key.value)
                    modifiers = sorted({*key_modifiers, "alt"})
                    yield events.Key(
                        "+".join([*modifiers, base_key]),
                        sequence,
                        modifiers=modifiers,
                        base_key=base_key,
                    )
                else:
                    yield events.Key(
                        key.value, sequence if len(sequence) == 1 else None
                    )
            return
        # If keys is a string, the intention is that it's a mapping to a
        # character, which should really be treated as the sequence for the
        # purposes of the next step...
        if isinstance(keys, str):
            sequence = keys
        # If the sequence is a single character, attempt to process it as a
        # key.
        if len(sequence) == 1:
            try:
                if not sequence.isalnum():
                    name = _character_to_key(sequence)
                else:
                    name = sequence

                name = KEY_NAME_REPLACEMENTS.get(name, name)
                if len(name) == 1 and alt:
                    if name.isupper():
                        name = f"shift+{name.lower()}"
                    name = f"alt+{name}"
                yield events.Key(name, sequence)
            except Exception:
                yield events.Key(sequence, sequence)

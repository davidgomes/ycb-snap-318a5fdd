"""Line magic for recording an IPython session bundle."""

# Copyright (c) IPython Development Team.
# Distributed under the terms of the Modified BSD License.

import argparse
import shlex

from IPython.core.error import UsageError
from IPython.core.magic import Magics, line_magic, magics_class


class _BundleParser(argparse.ArgumentParser):
    """ArgumentParser that raises UsageError instead of exiting."""

    def exit(self, status=0, message=None):
        if message:
            raise UsageError(message.rstrip())
        raise UsageError(f"{self.prog}: error")

    def error(self, message):
        raise UsageError(f"{self.prog}: {message}")


def _parser() -> argparse.ArgumentParser:
    parser = _BundleParser(prog="%session_bundle", add_help=False)
    subparsers = parser.add_subparsers(dest="command", required=True)

    start = subparsers.add_parser("start", add_help=False)
    start.add_argument("path", help="Bundle path to record into")
    start.add_argument(
        "--overwrite",
        action="store_true",
        help="Replace an existing bundle and start fresh",
    )
    start.add_argument(
        "--redact",
        action="append",
        default=[],
        metavar="PATTERN",
        help="Literal string to replace with <redacted> in recorded events",
    )

    subparsers.add_parser("status", add_help=False)
    subparsers.add_parser("stop", add_help=False)
    return parser


_PARSER = _parser()


@magics_class
class SessionBundleMagics(Magics):
    """Magics for recording and inspecting an IPython session bundle."""

    @line_magic
    def session_bundle(self, line=""):
        """Record this IPython session to a ``.ipybundle`` archive.

        Usage::

            %session_bundle start <path> [--overwrite] [--redact PATTERN]...
            %session_bundle status
            %session_bundle stop

        ``start`` begins recording cells executed after this call. It raises
        if a recording is already active. If ``<path>`` already exists,
        ``start`` raises ``FileExistsError`` unless ``--overwrite`` is given;
        with ``--overwrite`` the bundle is replaced and recording starts fresh.

        ``--redact PATTERN`` may be repeated. Each literal pattern is stored in
        bundle metadata, in the order given, and replaced with ``<redacted>``
        everywhere it occurs in ``events.jsonl``.

        ``status`` returns ``{"recording": bool, "path": str | None}``.

        ``stop`` finishes the archive and returns its path.
        """
        try:
            argv = shlex.split(line or "")
        except ValueError as exc:
            raise UsageError(f"%session_bundle: {exc}") from exc
        args = _PARSER.parse_args(argv)
        if args.command == "status":
            return self.shell.session_bundle_status()
        if args.command == "stop":
            return self.shell.stop_session_bundle()
        return self.shell.start_session_bundle(
            args.path,
            overwrite=args.overwrite,
            redact=args.redact,
        )

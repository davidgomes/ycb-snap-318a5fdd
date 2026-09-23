"""Line magic for recording and stopping IPython session bundles."""

import argparse
import shlex

from IPython.core.error import UsageError
from IPython.core.magic import Magics, line_magic, magics_class


class _UsageArgumentParser(argparse.ArgumentParser):
    """Argument parser that raises ``UsageError`` instead of exiting."""

    def error(self, message):
        raise UsageError(message)

    def exit(self, status=0, message=None):
        raise UsageError(message or f"%session_bundle failed with status {status}")


def _parser() -> argparse.ArgumentParser:
    parser = _UsageArgumentParser(prog="%session_bundle", add_help=False)
    subparsers = parser.add_subparsers(
        dest="command", required=True, parser_class=_UsageArgumentParser
    )

    start = subparsers.add_parser("start", add_help=False)
    start.add_argument("path")
    start.add_argument("--overwrite", action="store_true")
    start.add_argument("--redact", action="append", default=[])

    subparsers.add_parser("status", add_help=False)
    subparsers.add_parser("stop", add_help=False)
    return parser


@magics_class
class SessionBundleMagics(Magics):
    """Record an interactive session to a ``.ipybundle`` archive."""

    @line_magic
    def session_bundle(self, line=""):
        """Record cells to a session bundle, or inspect and stop recording.

        Usage::

            %session_bundle start <path> [--overwrite] [--redact PATTERN]...
            %session_bundle status
            %session_bundle stop

        ``status`` returns ``{"recording": bool, "path": str | None}``.
        ``start`` raises if a recording is already active, and raises
        ``FileExistsError`` when ``path`` exists unless ``--overwrite`` is
        given. Patterns passed to ``--redact`` are stored in order and
        replaced with ``<redacted>`` in the recorded events.
        """
        try:
            args = _parser().parse_args(shlex.split(line))
        except ValueError as exc:
            raise UsageError(str(exc)) from exc

        shell = self.shell
        if args.command == "start":
            return shell.start_session_bundle(
                args.path,
                overwrite=args.overwrite,
                redact=args.redact,
            )
        if args.command == "status":
            return shell.session_bundle_status()
        if args.command == "stop":
            return shell.stop_session_bundle()
        raise UsageError(
            "usage: %session_bundle start <path> [--overwrite] [--redact PATTERN]... "
            "| status | stop"
        )

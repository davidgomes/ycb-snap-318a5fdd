"""Implementation of the %session_bundle magic."""

import argparse
import sys

from IPython.core.error import UsageError
from IPython.core.magic import Magics, line_magic, magics_class
from IPython.utils.process import arg_split


class _ArgumentParser(argparse.ArgumentParser):
    def error(self, message):
        raise UsageError(f"%session_bundle: {message}")


def _make_parser():
    parser = _ArgumentParser(prog="%session_bundle", add_help=False)
    sub = parser.add_subparsers(dest="action", required=True)
    start = sub.add_parser("start", add_help=False)
    start.add_argument("path")
    start.add_argument("--overwrite", action="store_true")
    start.add_argument("--redact", action="append", default=[], metavar="PATTERN")
    sub.add_parser("status", add_help=False)
    sub.add_parser("stop", add_help=False)
    return parser


@magics_class
class SessionBundleMagics(Magics):
    """Magics to record a session to a bundle file."""

    @line_magic
    def session_bundle(self, line=""):
        """Record executed cells to a session bundle (``.ipybundle``) file.

        Usage::

          %session_bundle start <path> [--overwrite] [--redact PATTERN]...
          %session_bundle status
          %session_bundle stop

        ``start`` begins recording every following cell (source, stdout,
        stderr, expression result and error) to ``<path>``. It fails if a
        recording is already active, or if ``<path>`` exists and
        ``--overwrite`` is not given. Every ``--redact`` pattern is replaced
        by ``<redacted>`` in the recorded events.

        ``status`` returns ``{"recording": bool, "path": str | None}``.

        ``stop`` ends the recording and returns the bundle path.

        Bundles can be inspected and replayed with
        ``IPython.core.sessionbundle.load_session_bundle`` and
        ``IPython.core.sessionbundle.replay_session_bundle``.
        """
        args = _make_parser().parse_args(
            arg_split(line, posix=not sys.platform.startswith("win"))
        )
        if args.action == "start":
            return self.shell.start_session_bundle(
                args.path, overwrite=args.overwrite, redact=args.redact
            )
        if args.action == "stop":
            return self.shell.stop_session_bundle()
        return self.shell.session_bundle_status()

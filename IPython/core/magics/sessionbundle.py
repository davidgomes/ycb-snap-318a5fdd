"""Line magic for recording an IPython session bundle."""

# Copyright (c) IPython Development Team.
# Distributed under the terms of the Modified BSD License.

import shlex

from IPython.core.error import UsageError
from IPython.core.magic import Magics, magics_class, line_magic, no_var_expand
from IPython.core.magic_arguments import MagicArgumentParser


@magics_class
class SessionBundleMagics(Magics):
    """Record an interactive session into a replayable ``.ipybundle`` file."""

    @no_var_expand
    @line_magic
    def session_bundle(self, line=""):
        """Record a session to one bundle file, or inspect and stop recording.

        Usage::

            %session_bundle start <path> [--overwrite] [--redact PATTERN]...
            %session_bundle status
            %session_bundle stop

        ``start`` begins recording executed cells to ``path``. It fails if a
        recording is already active. If ``path`` already exists, pass
        ``--overwrite`` to replace it and start fresh.

        ``--redact PATTERN`` may be repeated. Each pattern is a literal string
        (not a regular expression) and is replaced with ``<redacted>``
        everywhere it occurs in the recorded events.

        ``status`` returns ``{"recording": bool, "path": str | None}``.

        ``stop`` finishes the bundle and returns its path.
        """
        parser = MagicArgumentParser(
            prog="%session_bundle",
            description="Record or replay-ready session bundles.",
        )
        subparsers = parser.add_subparsers(
            dest="command", required=True, parser_class=MagicArgumentParser
        )

        start = subparsers.add_parser("start", add_help=False)
        start.add_argument("path")
        start.add_argument("--overwrite", action="store_true")
        start.add_argument("--redact", action="append", default=[], metavar="PATTERN")

        subparsers.add_parser("status", add_help=False)
        subparsers.add_parser("stop", add_help=False)

        try:
            argv = shlex.split(line, posix=True) if line else []
        except ValueError as exc:
            raise UsageError(str(exc)) from exc
        args = parser.parse_args(argv)

        if args.command == "start":
            return self.shell.start_session_bundle(
                args.path,
                overwrite=args.overwrite,
                redact=args.redact,
            )
        if args.command == "status":
            return self.shell.session_bundle_status()
        if args.command == "stop":
            return self.shell.stop_session_bundle()
        raise parser.error(f"unknown session_bundle command {args.command!r}")

"""Implementation of the magic recording a session into a replayable bundle."""

# Copyright (c) IPython Development Team.
# Distributed under the terms of the Modified BSD License.

from typing import Any

from IPython.core import magic_arguments
from IPython.core.error import UsageError
from IPython.core.magic import Magics, line_magic, magics_class


@magics_class
class SessionBundleMagics(Magics):
    """Magics to record the session into a replayable bundle."""

    @magic_arguments.magic_arguments()
    @magic_arguments.argument(
        "action",
        choices=("start", "status", "stop"),
        help="Start or stop recording, or report whether a recording is active.",
    )
    @magic_arguments.argument(
        "path", nargs="?", help="The bundle file to record to (start only)."
    )
    @magic_arguments.argument(
        "--overwrite",
        action="store_true",
        help="Replace the bundle if it already exists (start only).",
    )
    @magic_arguments.argument(
        "--redact",
        action="append",
        metavar="PATTERN",
        help="Literal string to replace with <redacted> in the recorded cells; "
        "can be repeated (start only).",
    )
    @line_magic
    def session_bundle(self, line: str = "") -> str | dict[str, Any]:
        """Record the cells you run into a replayable session bundle.

        ``start PATH`` records the code, stdout, stderr, expression result and
        error of every following cell into the ``.ipybundle`` ZIP archive at
        PATH, which is rewritten after each cell. ``stop`` ends the recording;
        both return the bundle path. ``status`` returns
        ``{"recording": bool, "path": str | None}``.

        Inspect a bundle with ``IPython.core.sessionbundle.load_session_bundle``
        and run it again with ``replay_session_bundle``. For example::

            %session_bundle start analysis.ipybundle --redact hunter2
            password = "hunter2"
            %session_bundle stop
        """
        assert self.shell is not None
        args = magic_arguments.parse_argstring(self.session_bundle, line)
        if args.action == "start":
            if args.path is None:
                raise UsageError("%session_bundle start requires a bundle path")
            return self.shell.start_session_bundle(
                args.path, overwrite=args.overwrite, redact=args.redact
            )
        if args.path is not None or args.overwrite or args.redact:
            raise UsageError(f"%session_bundle {args.action} takes no arguments")
        if args.action == "stop":
            return self.shell.stop_session_bundle()
        return self.shell.session_bundle_status()

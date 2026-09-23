"""Line magic for recording and inspecting an IPython session bundle."""

from IPython.core.error import UsageError
from IPython.core.magic import Magics, line_magic, magics_class
from IPython.core.magic_arguments import argument, magic_arguments, parse_argstring


@magics_class
class SessionBundleMagics(Magics):
    """Record the current session into a single ``.ipybundle`` file."""

    @line_magic
    @magic_arguments()
    @argument("action", choices=("start", "status", "stop"))
    @argument("path", nargs="?")
    @argument("--overwrite", action="store_true")
    @argument("--redact", action="append", default=None, metavar="PATTERN")
    def session_bundle(self, line=""):
        """Record an IPython session to one file.

        %session_bundle start <path> [--overwrite] [--redact PATTERN]...
        %session_bundle status
        %session_bundle stop
        """
        args = parse_argstring(self.session_bundle, line)
        if args.action == "status":
            return self.shell.session_bundle_status()
        if args.action == "stop":
            return self.shell.stop_session_bundle()
        if not args.path:
            raise UsageError("%session_bundle start requires a path")
        return self.shell.start_session_bundle(
            args.path,
            overwrite=args.overwrite,
            redact=args.redact,
        )

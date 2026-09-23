"""Magic for recording IPython sessions into a session bundle."""

import argparse
import shlex

from ..error import UsageError
from ..magic import Magics, line_magic, magics_class


def _parser():
    p = argparse.ArgumentParser(prog="%session_bundle", add_help=False)
    sub = p.add_subparsers(dest="action")
    start = sub.add_parser("start", add_help=False)
    start.add_argument("path")
    start.add_argument("--overwrite", action="store_true")
    start.add_argument("--redact", action="append", default=[], metavar="PATTERN")
    sub.add_parser("status", add_help=False)
    sub.add_parser("stop", add_help=False)
    return p


@magics_class
class SessionBundleMagics(Magics):
    @line_magic
    def session_bundle(self, line=""):
        """Record the session to a ``.ipybundle`` file.

        Usage::

          %session_bundle start <path> [--overwrite] [--redact PATTERN]...
          %session_bundle status
          %session_bundle stop
        """
        try:
            args = _parser().parse_args(shlex.split(line))
        except SystemExit as e:
            raise UsageError(self.session_bundle.__doc__) from e
        if args.action == "start":
            return self.shell.start_session_bundle(
                args.path, overwrite=args.overwrite, redact=args.redact
            )
        if args.action == "stop":
            return self.shell.stop_session_bundle()
        if args.action == "status":
            return self.shell.session_bundle_status()
        raise UsageError(self.session_bundle.__doc__)

import argparse
import json

from IPython.core.magic import Magics, magics_class, line_magic


@magics_class
class SessionBundleMagics(Magics):
    @line_magic
    def session_bundle(self, line=""):
        parser = argparse.ArgumentParser(prog="%session_bundle")
        subparsers = parser.add_subparsers(dest="command", required=True)
        start = subparsers.add_parser("start")
        start.add_argument("path")
        start.add_argument("--overwrite", action="store_true")
        start.add_argument("--redact", action="append", default=[])
        subparsers.add_parser("status")
        subparsers.add_parser("stop")
        args = parser.parse_args(line.split())
        if args.command == "start":
            return self.shell.start_session_bundle(
                args.path, overwrite=args.overwrite, redact=args.redact
            )
        if args.command == "stop":
            return self.shell.stop_session_bundle()
        return json.dumps(self.shell.session_bundle_status())

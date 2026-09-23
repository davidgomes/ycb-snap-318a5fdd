Session bundles
===============

The new ``%session_bundle start <path> [--overwrite] [--redact PATTERN]...``
magic (with ``status`` and ``stop``) records every executed cell -- source,
stdout, stderr, expression result and error -- to a single ``.ipybundle`` ZIP
file. The same is available programmatically via
``InteractiveShell.start_session_bundle`` / ``stop_session_bundle`` /
``session_bundle_status``, and ``IPython.core.sessionbundle`` provides
``load_session_bundle``, ``replay_session_bundle``, ``validate_session_bundle``,
``save_session_bundle`` and the ``session_bundle_recorder`` context manager.

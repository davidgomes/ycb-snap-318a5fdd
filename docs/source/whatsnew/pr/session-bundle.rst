Session bundles
===============

The new ``%session_bundle`` magic records a session to a single
``.ipybundle`` file (a ZIP archive with ``metadata.json`` and
``events.jsonl``) that can later be inspected or replayed::

    In [1]: %session_bundle start analysis.ipybundle --redact my-api-token
    In [2]: ...
    In [3]: %session_bundle stop

Each executed cell is stored with its source, stdout, stderr, result and
error, if any. ``--redact PATTERN`` (repeatable) replaces the given literal
strings with ``<redacted>`` in the recording, and ``--overwrite`` replaces an
existing bundle. ``%session_bundle status`` reports whether a recording is
active.

The same functionality is available programmatically through
``InteractiveShell.start_session_bundle``, ``stop_session_bundle`` and
``session_bundle_status``, and :mod:`IPython.core.sessionbundle` provides
``load_session_bundle``, ``replay_session_bundle``, ``save_session_bundle``,
``validate_session_bundle`` and the ``session_bundle_recorder`` context
manager.

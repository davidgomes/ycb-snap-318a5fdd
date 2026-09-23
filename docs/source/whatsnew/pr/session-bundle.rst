Session bundles
===============

The new ``%session_bundle`` magic records the cells you run into a single
``.ipybundle`` file that can be inspected or replayed later::

    %session_bundle start analysis.ipybundle --redact hunter2
    ...
    %session_bundle status
    %session_bundle stop

A bundle is a ZIP archive holding ``metadata.json`` and ``events.jsonl``, with
one JSON event per executed cell: its code, stdout, stderr, expression result
and, for failed cells, the error and traceback. Each ``--redact`` string is
replaced with ``<redacted>`` in the recorded events.

The same recording is available programmatically through
``InteractiveShell.start_session_bundle``, ``stop_session_bundle`` and
``session_bundle_status``, and :mod:`IPython.core.sessionbundle` provides
``load_session_bundle``, ``replay_session_bundle``, ``save_session_bundle``,
``validate_session_bundle`` and the ``session_bundle_recorder`` context manager.

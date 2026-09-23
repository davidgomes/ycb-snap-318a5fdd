.. _validated:

Validated
=========

``Validated`` collects every independent failure instead of stopping
at the first one.
Use it when several inputs are checked separately and the caller needs
the full set of errors.

``Validated`` has two concrete types: ``Valid`` and ``Invalid``.
``Valid`` holds a success value.
``Invalid`` holds an immutable tuple of errors.

.. code:: python

  >>> from returns.validated import Invalid, Valid, Validated

  >>> def parse_int(raw: str) -> Validated[int, str]:
  ...     if raw.isdigit():
  ...         return Valid(int(raw))
  ...     return Validated.from_failure(raw + ' is not an int')

  >>> assert Validated.combine(
  ...     parse_int('1'),
  ...     parse_int('x'),
  ...     lambda left, right: left + right,
  ... ) == Invalid(('x is not an int',))

``bind`` still short-circuits.
``apply`` is what concatenates error tuples from left to right.


Pattern Matching
----------------

``Valid`` and ``Invalid`` support structural pattern matching:

.. code:: python

  >>> from returns.validated import Invalid, Valid

  >>> def label(container):
  ...     match container:
  ...         case Valid(value):
  ...             return value
  ...         case Invalid(errors):
  ...             return errors

  >>> assert label(Valid(1)) == 1
  >>> assert label(Invalid(('a', 'b'))) == ('a', 'b')


API Reference
-------------

.. automodule:: returns.validated
   :members:

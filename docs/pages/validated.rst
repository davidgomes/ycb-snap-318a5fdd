.. _validated:

Validated
=========

``Validated`` is a container for checks that should report every independent
failure, not only the first one.

It has two concrete types: ``Valid`` and ``Invalid``.
``Invalid`` stores an immutable tuple of errors.
:meth:`~returns.validated.Validated.from_failure` wraps one error into a
1-tuple so later steps can concatenate those tuples.

``bind`` still short-circuits.
``apply`` concatenates errors from both sides, this container first.

.. code:: python

  >>> from returns.validated import Invalid, Valid, Validated

  >>> def positive(number: int) -> Validated[int, str]:
  ...     if number > 0:
  ...         return Valid(number)
  ...     return Invalid(('must be positive',))

  >>> def even(number: int) -> Validated[int, str]:
  ...     if number % 2 == 0:
  ...         return Valid(number)
  ...     return Invalid(('must be even',))

Use :meth:`~returns.validated.Validated.combine` when both checks are
independent:

.. code:: python

  >>> from returns.validated import Invalid, Valid, Validated

  >>> def positive(number: int) -> Validated[int, str]:
  ...     if number > 0:
  ...         return Valid(number)
  ...     return Invalid(('must be positive',))

  >>> def even(number: int) -> Validated[int, str]:
  ...     if number % 2 == 0:
  ...         return Valid(number)
  ...     return Invalid(('must be even',))

  >>> def both(left: int, right: int) -> tuple[int, int]:
  ...     return (left, right)

  >>> assert Validated.combine(positive(2), even(4), both) == Valid((2, 4))
  >>> assert Validated.combine(positive(-1), even(3), both) == Invalid((
  ...     'must be positive',
  ...     'must be even',
  ... ))

:meth:`~returns.validated.Validated.combine_n` does the same for any number
of containers.

Pattern Matching
----------------

``Valid`` and ``Invalid`` support structural pattern matching.
``Invalid`` matches the error tuple as a single value.

.. code:: python

  >>> from returns.validated import Invalid, Valid

  >>> def label(container: Valid | Invalid) -> str:
  ...     match container:
  ...         case Valid(value):
  ...             return f'ok {value}'
  ...         case Invalid(errors):
  ...             return ','.join(errors)

  >>> assert label(Valid(1)) == 'ok 1'
  >>> assert label(Invalid(('a', 'b'))) == 'a,b'

Decorator
---------

:func:`validated <returns.validated.validated>` catches exceptions
and returns ``Invalid((exception,))``.
It keeps the wrapped function's name.

.. code:: python

  >>> from returns.validated import Valid, validated

  >>> @validated(exceptions=(ZeroDivisionError,))
  ... def divide(number: int) -> float:
  ...     return 1 / number

  >>> assert divide(1) == Valid(1.0)
  >>> assert divide.__name__ == 'divide'

Converting from ``Result``
--------------------------

:meth:`~returns.validated.Validated.from_result` and
:func:`returns.converters.result_to_validated` turn ``Success`` into ``Valid``
and wrap a ``Failure`` error into a 1-tuple.
:func:`returns.converters.validated_to_result` turns that tuple into the
``Failure`` value.

API Reference
-------------

.. autoclass:: returns.validated.Validated
   :members:

.. autoclass:: returns.validated.Valid
   :members:

.. autoclass:: returns.validated.Invalid
   :members:

.. autofunction:: returns.validated.validated

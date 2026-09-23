.. _validated:

Validated
=========

``Validated`` is a container for error-accumulating validation.
It is very similar to :ref:`Result <result>`,
but it does not stop at the first failure when you combine
several independent values together: all errors are collected instead.

``Validated`` consist of two types: ``Valid`` and ``Invalid``.
``Valid`` holds a successful value,
``Invalid`` holds a tuple of all collected errors.

.. code:: python

  >>> from returns.validated import Invalid, Valid, Validated

  >>> assert Validated.from_value(1) == Valid(1)
  >>> assert Validated.from_failure('error') == Invalid(('error',))


Accumulating errors
-------------------

Errors are accumulated by ``.apply``, errors of both containers
are joined in a stable left-to-right order.
The easiest way to use it is :meth:`~returns.validated.Validated.combine`
and :meth:`~returns.validated.Validated.combine_n` methods:

.. code:: python

  >>> from returns.validated import Invalid, Valid, Validated

  >>> def validate_name(name: str) -> Validated[str, str]:
  ...     if not name:
  ...         return Invalid(('Name is empty',))
  ...     return Valid(name)

  >>> def validate_age(age: int) -> Validated[int, str]:
  ...     if age < 0:
  ...         return Invalid(('Age is negative',))
  ...     return Valid(age)

  >>> def create_user(name: str, age: int) -> tuple[str, int]:
  ...     return (name, age)

  >>> assert Validated.combine(
  ...     validate_name('Alice'), validate_age(30), create_user,
  ... ) == Valid(('Alice', 30))

  >>> assert Validated.combine(
  ...     validate_name(''), validate_age(-1), create_user,
  ... ) == Invalid(('Name is empty', 'Age is negative'))

:meth:`returns.iterables.Fold.collect` accumulates all errors as well:

.. code:: python

  >>> from returns.iterables import Fold

  >>> assert Fold.collect(
  ...     [Valid(1), Invalid(('a',)), Invalid(('b',))],
  ...     Valid(()),
  ... ) == Invalid(('a', 'b'))

Dependent steps, like ``.bind`` and :ref:`do-notation`,
still short-circuit on the first failure,
because the next step requires a value from the previous one.


Pattern Matching
----------------

``Valid`` and ``Invalid`` values can be matched
with `Structural Pattern Matching <https://www.python.org/dev/peps/pep-0622/>`_:

.. code:: python

  >>> from returns.validated import Invalid, Valid

  >>> match Invalid(('a', 'b')):
  ...     case Valid(value):
  ...         print(value)
  ...     case Invalid(errors):
  ...         print(errors)
  ('a', 'b')


Decorators
----------

:func:`validated <returns.validated.validated>` works like
:func:`safe <returns.result.safe>`,
but returns ``Invalid`` with a single caught exception:

.. code:: python

  >>> from returns.validated import Valid, validated

  >>> @validated(exceptions=(ZeroDivisionError,))
  ... def divide(number: int) -> float:
  ...     return 1 / number

  >>> assert divide(1) == Valid(1.0)
  >>> assert isinstance(divide(0).failure()[0], ZeroDivisionError)


API Reference
-------------

.. autoclasstree:: returns.validated
   :strict:

.. automodule:: returns.validated
   :members:

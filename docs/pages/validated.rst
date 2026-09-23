Validated
=========

``Validated`` is a container that accumulates errors.
It consists of two types: ``Valid`` and ``Invalid``.

It is useful when validating multiple independent inputs:
instead of stopping at the first failure like ``Result`` does,
it collects all errors.

``Invalid`` always stores its errors as an immutable ``tuple``.

.. code:: python

  >>> from returns.validated import Invalid, Valid, Validated

  >>> def validate_name(name: str) -> Validated[str, str]:
  ...     if not name:
  ...         return Validated.from_failure('Name is empty')
  ...     return Valid(name)

  >>> def validate_age(age: int) -> Validated[int, str]:
  ...     if age < 0:
  ...         return Validated.from_failure('Age is negative')
  ...     return Valid(age)

  >>> def create_user(name: str, age: int) -> dict[str, object]:
  ...     return {'name': name, 'age': age}

  >>> assert Validated.combine(
  ...     validate_name('Alice'), validate_age(30), create_user,
  ... ) == Valid({'name': 'Alice', 'age': 30})

  >>> assert Validated.combine(
  ...     validate_name(''), validate_age(-1), create_user,
  ... ) == Invalid(('Name is empty', 'Age is negative'))

Accumulation happens in ``.apply``,
errors are kept in a stable left-to-right order.
``.bind`` still short-circuits on the first failure,
because the next computation depends on the previous value.

Use :meth:`~returns.validated.Validated.combine_n`
to combine any number of containers with an N-ary function.

Converting from and to Result
-----------------------------

Use :meth:`~returns.validated.Validated.from_result`
or :func:`returns.converters.result_to_validated`
to convert ``Result`` to ``Validated``,
and :func:`returns.converters.validated_to_result` for the opposite direction.

.. code:: python

  >>> from returns.converters import result_to_validated, validated_to_result
  >>> from returns.result import Failure, Success

  >>> assert result_to_validated(Failure('a')) == Invalid(('a',))
  >>> assert validated_to_result(Invalid(('a', 'b'))) == Failure(('a', 'b'))

validated decorator
-------------------

:func:`~returns.validated.validated` works like
:func:`returns.result.safe`, but returns ``Invalid`` with a caught exception.

.. code:: python

  >>> from returns.validated import validated

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

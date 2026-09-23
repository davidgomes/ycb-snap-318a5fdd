.. _validated:

Validated
=========

:class:`Validated <returns.validated.Validated>` collects every independent
failure instead of stopping at the first one.
It has two concrete types: :class:`~returns.validated.Valid` and
:class:`~returns.validated.Invalid`.

``Invalid`` stores errors as an immutable tuple.
The second type argument is a single error element.

.. code:: python

  >>> from returns.validated import Invalid, Valid, Validated

  >>> assert str(Validated.from_value(1)) == '<Valid: 1>'
  >>> assert str(Validated.from_failure('e')) == "<Invalid: ('e',)>"


Accumulation and bind
---------------------

:meth:`~returns.validated.Validated.apply` concatenates errors from left
to right. :meth:`~returns.validated.Validated.bind` still short-circuits.

.. code:: python

  >>> from returns.validated import Invalid, Valid

  >>> assert Invalid((1,)).apply(Invalid((2,))) == Invalid((1, 2))
  >>> assert Invalid(('kept',)).bind(lambda value: Invalid(('new',))) == Invalid(('kept',))

:meth:`~returns.validated.Validated.combine` and
:meth:`~returns.validated.Validated.combine_n` use the same applicative
combination:

.. code:: python

  >>> from returns.validated import Invalid, Valid, Validated

  >>> assert Validated.combine(Valid(2), Valid(3), lambda left, right: left + right) == Valid(5)
  >>> assert Validated.combine_n(
  ...     (Invalid(('a',)), Valid(1), Invalid(('b',))),
  ...     lambda left, middle, right: left,
  ... ) == Invalid(('a', 'b'))

:meth:`~returns.validated.Invalid.alt` maps each error element:

.. code:: python

  >>> from returns.validated import Invalid, Valid

  >>> assert Invalid((1, 2)).alt(str) == Invalid(('1', '2'))
  >>> assert Valid(1).alt(str) == Valid(1)


Swap
----

:meth:`~returns.validated.Validated.swap` turns ``Valid(x)`` into
``Invalid((x,))`` and ``Invalid(errors)`` into ``Valid(errors)``.
Swapping twice wraps the original success value in a tuple.

.. code:: python

  >>> from returns.validated import Invalid, Valid

  >>> assert Valid(1).swap() == Invalid((1,))
  >>> assert Invalid((1, 2)).swap() == Valid((1, 2))


Decorator
---------

:func:`~returns.validated.validated` catches exceptions and returns
``Invalid``. It keeps the wrapped function's name.

.. code:: python

  >>> from returns.validated import Valid, validated

  >>> @validated
  ... def divide(number: int) -> float:
  ...     return 1 / number

  >>> assert divide.__name__ == 'divide'
  >>> assert divide(1) == Valid(1.0)


Pattern matching
----------------

Both containers support structural pattern matching via ``__match_args__``.

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

.. automodule:: returns.interfaces.specific.validated
   :members:

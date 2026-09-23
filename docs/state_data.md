(state-data)=
# State data

```{versionadded} 3.1.0
```

A {ref}`State` can own data: variables that exist only while the state is active.
Declare them with the `data` keyword, a dict mapping string keys to default values.

- On **entry**, the state's data is initialized with a fresh copy of the defaults.
- On **exit**, the data is removed.
- **Re-entering** a state resets its data to the original defaults.
- Data is stored **per instance** — the `State` declaration is shared by all
  instances of the class and is never mutated.

```py
>>> from statemachine import DataVar, State, StateChart

>>> class Checkout(StateChart):
...     browsing = State(initial=True)
...     cart = State(data={"items": list, "coupon": None})
...     paid = State(final=True, data={"receipt": DataVar(factory=dict)})
...
...     open_cart = browsing.to(cart)
...     add = cart.to.itself(internal=True, on="add_item")
...     keep_browsing = cart.to(browsing)
...     pay = cart.to(paid)
...
...     def add_item(self, item, state_data):
...         state_data["items"] = state_data["items"] + [item]

>>> sm = Checkout()
>>> sm.send("open_cart")
>>> sm.send("add", item="ring")
>>> sm.get_state_data("cart")
{'items': ['ring'], 'coupon': None}

>>> sm.send("keep_browsing")
>>> sm.get_state_data("cart") is None
True

>>> sm.send("open_cart")
>>> sm.get_state_data("cart")
{'items': [], 'coupon': None}

```

## Declaring variables

Each value in the `data` dict can be:

- A plain value: used as the default. Every entry receives a (deep) copy, so mutable
  defaults are never shared.
- A callable: used as a factory, called on every entry to produce a fresh value
  (e.g. `list`, `dict`, or a `lambda`).
- A {class}`~statemachine.state_data.DataVar`: an explicit declaration that supports
  an optional `type` constraint and either a `default` or a `factory` (not both).

```py
>>> DataVar(0, type=int)
DataVar(default=0, type=int)

>>> DataVar(0, factory=int)
Traceback (most recent call last):
...
statemachine.exceptions.InvalidDefinition: DataVar accepts either 'default' or 'factory', ...

>>> State(data=["items"])
Traceback (most recent call last):
...
statemachine.exceptions.InvalidDefinition: State 'data' must be a dict mapping string keys ...

```

Compound and parallel states accept `data` as a class keyword:

```py
>>> class Journey(StateChart):
...     class shire(State.Compound, data={"hobbits": 4}):
...         bag_end = State(initial=True, data={"rings": 1})
...         road = State(final=True)
...         depart = bag_end.to(road)

>>> Journey().state_data_values
{'shire': {'hobbits': 4}, 'bag_end': {'rings': 1}}

```

## Using data in callbacks

Callbacks can ask for the `state_data` parameter, injected alongside `source`,
`target`, `event_data` and the other {ref}`dynamic-dispatch` arguments.
`state_data` is a mapping that **merges the data of the state and its active
ancestors**: on key collisions, the child shadows the parent. Parallel regions are
isolated from each other, as sibling regions are not ancestors of one another.

Assigning a key through `state_data` updates the innermost active state that
declares it, enforcing any `DataVar` type. Data stays available during both
`on_enter` and `on_exit` callbacks.

```py
>>> class Scoped(StateChart):
...     class shire(State.Compound, data={"name": "The Shire", "visits": 0}):
...         bag_end = State(initial=True, data={"name": "Bag End"})
...         garden = State(final=True)
...         walk = bag_end.to(garden)
...
...     def on_enter_bag_end(self, state_data):
...         print(dict(state_data))
...
...     def on_exit_bag_end(self, state_data):
...         state_data["visits"] += 1

>>> sm = Scoped()
{'name': 'Bag End', 'visits': 0}

>>> sm.send("walk")
>>> sm.get_state_data("shire")
{'name': 'The Shire', 'visits': 1}

```

For `on_enter_*` and `on_exit_*` callbacks the scope is the state being entered or
exited. For transition callbacks it is the transition's source (or the target, for
`after` callbacks).

## History

When a state is recalled through a {ref}`history state <history-states>`, its data is restored from
the snapshot taken when it was exited: a deep history restores the data of all the
remembered descendants, while a shallow history restores the direct children only.

```py
>>> from statemachine import HistoryState

>>> class Reader(StateChart):
...     class book(State.Compound):
...         chapter = State(initial=True, data={"page": 1})
...         h = HistoryState()
...
...     library = State()
...     close = book.to(library)
...     reopen = library.to(book.h)

>>> sm = Reader()
>>> sm.set_state_data("chapter", "page", 42)
>>> sm.send("close")
>>> sm.send("reopen")
>>> sm.get_state_data("chapter")
{'page': 42}

```

## Runtime API

- {meth}`~statemachine.statemachine.StateChart.get_state_data` returns the data dict
  of an active state (by `State` or id), or `None`.
- {attr}`~statemachine.statemachine.StateChart.state_data_values` returns a snapshot
  of all active data, keyed by state id.
- {meth}`~statemachine.statemachine.StateChart.set_state_data` assigns a value,
  raising `InvalidDefinition` if the state is not active, the key is not declared,
  or the value violates the `DataVar` type.
- {meth}`~statemachine.statemachine.StateChart.get_data_changes` returns the
  {class}`~statemachine.state_data.DataChangeInfo` records (`state_id`, `key`,
  `old_value`, `new_value`) accumulated during the current macrostep. The list is
  cleared at each macrostep boundary.

```py
>>> class Meter(StateChart):
...     running = State(initial=True, data={"reading": DataVar(0, type=int)})
...     stopped = State(final=True)
...     tick = running.to.itself(internal=True, on="increase")
...     stop = running.to(stopped)
...
...     def increase(self, state_data):
...         state_data["reading"] += 5

>>> sm = Meter()
>>> sm.send("tick")
>>> sm.get_data_changes()
[DataChangeInfo(state_id='running', key='reading', old_value=0, new_value=5)]

>>> sm.set_state_data("running", "reading", "high")
Traceback (most recent call last):
...
statemachine.exceptions.InvalidDefinition: Data variable 'reading' expects a value of type int, ...

>>> sm.send("stop")
>>> sm.get_data_changes()
[]

```

State data is part of the instance state, so it survives `pickle`.

## Diagrams and SCXML

{ref}`Diagrams <diagram>` annotate each state with its declared variables, as a
`data / ...` compartment line.

When loading SCXML, the `<data>` items of a state's own `<datamodel>` whose `expr`
is a Python literal become that state's data (they are still initialized in the
document-wide datamodel as well):

```xml
<state id="cart">
  <datamodel>
    <data id="items" expr="[]"/>
  </datamodel>
</state>
```

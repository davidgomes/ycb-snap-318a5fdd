(states)=
(state)=

# States

```{seealso}
New to statecharts? See [](concepts.md) for an overview of how states,
transitions, events, and actions fit together.
```

A **state** represents a distinct mode or condition of the system at a given
point in time. States are the building blocks of a statechart — you define them
as class attributes, and the library handles initialization, validation, and
lifecycle management.

```py
>>> from statemachine import State, StateChart

>>> class TrafficLight(StateChart):
...     green = State(initial=True)
...     yellow = State()
...     red = State()
...
...     cycle = green.to(yellow) | yellow.to(red) | red.to(green)

>>> sm = TrafficLight()
>>> "green" in sm.configuration_values
True

```


## State parameters

| Parameter | Default | Description |
|---|---|---|
| `name` | `""` | Human-readable display name. Defaults to the attribute name, capitalized. |
| `value` | `None` | Custom value for this state, accessible via `configuration_values`. |
| `initial` | `False` | Marks this as the initial state. Exactly one per machine (or per compound). |
| `final` | `False` | Marks this as a final (accepting) state. No outgoing transitions allowed. |
| `enter` | `None` | Callback(s) to run when entering this state. See {ref}`state-actions`. |
| `exit` | `None` | Callback(s) to run when leaving this state. See {ref}`state-actions`. |
| `invoke` | `None` | Background work spawned on entry, cancelled on exit. See {ref}`invoke-actions`. |
| `data` | `None` | Variables owned by this state, initialized on entry and discarded on exit. See {ref}`state-data`. |

```py
>>> class CampaignMachine(StateChart):
...     draft = State("Draft", value=1, initial=True)
...     producing = State("Being produced", value=2)
...     closed = State("Closed", value=3, final=True)
...
...     produce = draft.to(producing)
...     deliver = producing.to(closed)

>>> sm = CampaignMachine()
>>> sm.send("produce")
>>> list(sm.configuration_values)
[2]

```


## Initial state

A {ref}`StateChart` must have exactly one `initial` state. The initial state is
entered when the machine starts, and the corresponding {ref}`enter actions
<state-actions>` are called.


(final-state)=

## Final state

A **final** state signals that the machine has completed its work. No outgoing
transitions are allowed from a final state.

```py
>>> sm = CampaignMachine()
>>> sm.send("produce")
>>> sm.send("deliver")
>>> sm.is_terminated
True

```

You can query the list of all declared final states:

```py
>>> sm.final_states
[State('Closed', id='closed', value=3, initial=False, final=True, parallel=False)]

```

```{seealso}
See {ref}`validations` for the checks the library performs at class definition
time — including final state reachability, unreachable states, and trap states.
```


(compound-states)=

## Compound states

```{versionadded} 3.0.0
```

Compound states contain inner child states, enabling hierarchical state machines.
Define them using the `State.Compound` inner class syntax:

```py
>>> from statemachine import State, StateChart

>>> class Journey(StateChart):
...     class shire(State.Compound):
...         bag_end = State(initial=True)
...         green_dragon = State()
...         visit_pub = bag_end.to(green_dragon)
...     road = State(final=True)
...     depart = shire.to(road)

>>> sm = Journey()
>>> set(sm.configuration_values) == {"shire", "bag_end"}
True

```

Entering a compound activates both the parent and its `initial` child. You can query
whether a state is compound using the `is_compound` property.

```{seealso}
See {ref}`done-state-events` for completion events when a compound state's
final child is reached.
```


(parallel-states)=

## Parallel states

```{versionadded} 3.0.0
```

Parallel states activate all child regions simultaneously. Each region operates
independently. Define them using `State.Parallel`:

```py
>>> from statemachine import State, StateChart

>>> class WarOfTheRing(StateChart):
...     class war(State.Parallel):
...         class quest(State.Compound):
...             start = State(initial=True)
...             end = State(final=True)
...             go = start.to(end)
...         class battle(State.Compound):
...             fighting = State(initial=True)
...             won = State(final=True)
...             victory = fighting.to(won)

>>> sm = WarOfTheRing()
>>> "start" in sm.configuration_values and "fighting" in sm.configuration_values
True

```

```{seealso}
See {ref}`done-state-events` for how `done.state` events work with parallel
states (all regions must reach a final state).
```


(history-states)=

## History pseudo-states

```{versionadded} 3.0.0
```

A history pseudo-state records the active child of a compound state when it is exited.
Re-entering via the history state restores the previously active child. Import and use
`HistoryState` inside a `State.Compound`:

```py
>>> from statemachine import HistoryState, State, StateChart

>>> class WithHistory(StateChart):
...     class mode(State.Compound):
...         a = State(initial=True)
...         b = State()
...         h = HistoryState()
...         switch = a.to(b)
...     outside = State()
...     leave = mode.to(outside)
...     resume = outside.to(mode.h)

>>> sm = WithHistory()
>>> sm.send("switch")
>>> sm.send("leave")
>>> sm.send("resume")
>>> "b" in sm.configuration_values
True

```

Use `HistoryState(type="deep")` for deep history that remembers the exact leaf state
in nested compounds.


```{seealso}
See {ref}`querying-configuration` for how to inspect which states are currently
active at runtime.
```


(state-data)=

## State data

```{versionadded} 3.1.0
```

A state can own data: variables that only exist while the state is active. Declare
them with the `data` keyword, mapping each variable name to its default value:

```py
>>> from statemachine import DataVar, State, StateChart

>>> class Download(StateChart):
...     idle = State(initial=True)
...     downloading = State(data={"retries": 0, "chunks": list})
...     done = State(final=True)
...
...     start = idle.to(downloading)
...     retry = downloading.to.itself(internal=True, on="count_retry")
...     cancel = downloading.to(idle)
...     finish = downloading.to(done)
...
...     def count_retry(self, state_data):
...         state_data["retries"] += 1

>>> sm = Download()
>>> sm.get_state_data("downloading") is None
True

>>> sm.send("start")
>>> sm.get_state_data("downloading")
{'retries': 0, 'chunks': []}

>>> sm.send("retry")
>>> sm.send("retry")
>>> sm.get_state_data("downloading")["retries"]
2

```

The data lifecycle follows the state:

- **On entry**, the data is initialized with a fresh copy of the defaults, before any
  `on_enter` callback runs. Mutable defaults are never shared between entries or
  between machine instances.
- **On exit**, the data is discarded, after the `on_exit` callbacks run.
- **Re-entering** the state resets the data to the defaults.

```py
>>> sm.send("cancel")
>>> sm.get_state_data("downloading") is None
True

>>> sm.send("start")
>>> sm.get_state_data("downloading")
{'retries': 0, 'chunks': []}

```

The declarations live on the state definition, shared by all instances, while the
values are stored on each state machine instance.

### Declaring variables

Each entry of `data` can be:

- A plain value, used as the default (deep-copied on every entry).
- A plain callable, used as a factory called on every entry (e.g. `list`, `dict`).
- A {class}`~statemachine.state_data.DataVar`, which also supports an optional `type`,
  enforced on the initial value and on assignments, and an explicit `factory`.

```py
>>> DataVar(0, type=int)
DataVar(0, type=int)

>>> DataVar(factory=list)
DataVar(factory=list)

```

Invalid declarations raise {class}`~statemachine.exceptions.InvalidDefinition`: `data`
must be a dict with string keys, and a `DataVar` can't have both a `default` and a
`factory`.

```py
>>> DataVar(0, factory=int)
Traceback (most recent call last):
...
statemachine.exceptions.InvalidDefinition: DataVar accepts either 'default' or 'factory', not both.

>>> State(data=["retries"])
Traceback (most recent call last):
...
statemachine.exceptions.InvalidDefinition: State 'data' must be a dict, got ['retries'].

```

Compound and parallel states accept `data` as a class keyword, next to `name`.

### Accessing data from callbacks

Declare a `state_data` parameter to receive the data visible from the callback's
state. Data is scoped hierarchically: a state sees its own data merged with the data
of its active ancestors, and on a name collision the innermost state wins.
Assigning a key updates the state that owns it.

```py
>>> class Order(StateChart):
...     class checkout(State.Compound, data={"total": 0, "step": "checkout"}):
...         cart = State(initial=True, data={"items": list, "step": "cart"})
...         payment = State(data={"attempts": DataVar(0, type=int)})
...
...         add = cart.to.itself(internal=True, on="add_item")
...         pay = cart.to(payment)
...
...     done = State(final=True)
...     finish = checkout.to(done)
...
...     def add_item(self, price, state_data):
...         state_data["items"].append(price)
...         state_data["total"] += price
...
...     def on_enter_payment(self, state_data):
...         print(dict(state_data))

>>> sm = Order()
>>> sm.send("add", price=10)
>>> sm.send("add", price=5)
>>> sm.send("pay")
{'total': 15, 'step': 'checkout', 'attempts': 0}

```

Parallel regions are isolated: a region only sees its own data and the data of its
ancestors, never the data of a sibling region.

Transition callbacks see the scope of their `state` parameter (the source for
`cond`, `before` and `on`, the target for `after`), while each `on_exit` callback sees
the data of the state being exited.

### Querying and updating data

| Member | Description |
|---|---|
| `get_state_data(state)` | The live data dict of an active state, or `None`. |
| `set_state_data(state, key, value)` | Assign a variable, validating that the state is active, declares `key` and that `value` satisfies the `DataVar` type. |
| `state_data_values` | A snapshot of the data of all active states, by state id. |
| `get_data_changes()` | {class}`~statemachine.state_data.DataChangeInfo` records of the assignments made during the current macrostep. |

Violations raise {class}`~statemachine.exceptions.InvalidDefinition`:

```py
>>> sm = Order()
>>> sm.set_state_data("payment", "attempts", 1)
Traceback (most recent call last):
...
statemachine.exceptions.InvalidDefinition: Cannot set data variable 'attempts' of the inactive state 'payment'.

>>> sm.send("add", price=3)
>>> sm.get_data_changes()
[DataChangeInfo(state_id='checkout', key='total', old_value=0, new_value=3)]

>>> sm.state_data_values
{'checkout': {'total': 3, 'step': 'checkout'}, 'cart': {'items': [3], 'step': 'cart'}}

```

Every assignment made through `state_data` or `set_state_data()` is recorded, and the
records are cleared when the next macrostep starts.

### Data and history

Re-entering a compound state through a {ref}`history pseudo-state <history-states>`
restores the data saved when it was exited, instead of the defaults: deep history
restores the data of all the remembered descendants, shallow history only the data
of the direct children.

```py
>>> from statemachine import HistoryState

>>> class Editor(StateChart):
...     class editing(State.Compound):
...         draft = State(initial=True, data={"text": ""})
...         h = HistoryState()
...
...         write = draft.to.itself(internal=True, on="append")
...
...     saving = State()
...
...     save = editing.to(saving)
...     resume = saving.to(editing.h)
...
...     def append(self, char, state_data):
...         state_data["text"] += char

>>> sm = Editor()
>>> sm.send("write", char="h")
>>> sm.send("write", char="i")
>>> sm.send("save")
>>> sm.send("resume")
>>> sm.get_state_data("draft")
{'text': 'hi'}

```

State data survives pickling. Diagrams list the declared variables of each state,
and SCXML `<data>` elements inside a state's own `<datamodel>` whose `expr` is a
Python literal become that state's data.


(states from enum types)=

## States from Enum types

{ref}`States` can also be declared from standard `Enum` classes.

For this, use {ref}`States (class)` to convert your `Enum` type to a list of {ref}`State` objects.


```{eval-rst}
.. automethod:: statemachine.states.States.from_enum
  :noindex:
```

```{seealso}
See the example {ref}`sphx_glr_auto_examples_enum_campaign_machine.py`.
```

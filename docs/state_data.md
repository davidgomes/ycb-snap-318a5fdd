(state data)=

# State data

A state can own data. Pass a `data` mapping of variable names to default values
and the state takes care of the lifecycle:

- On **entry**, the data is initialized as a fresh copy of the defaults.
- On **exit**, the data is discarded.
- **Re-entering** the state starts again from the defaults.

Values live on each machine instance, never on the shared {ref}`State` declaration.

```py
>>> from statemachine import DataVar, State, StateChart

>>> class Checkout(StateChart):
...     browsing = State(initial=True)
...     cart = State(data={"items": list, "coupon": None})
...     paid = State(final=True)
...
...     open_cart = browsing.to(cart)
...     keep_browsing = cart.to(browsing)
...     pay = cart.to(paid)
...
...     def on_add(self, state_data, item):
...         state_data["items"].append(item)

>>> sm = Checkout()
>>> sm.get_state_data("cart") is None
True

>>> sm.send("open_cart")
>>> sm.get_state_data("cart")
{'items': [], 'coupon': None}

>>> sm.get_state_data("cart")["items"].append("book")
>>> sm.set_state_data("cart", "coupon", "SAVE10")
>>> sm.state_data_values
{'cart': {'items': ['book'], 'coupon': 'SAVE10'}}

>>> sm.send("keep_browsing")
>>> sm.state_data_values
{}

>>> sm.send("open_cart")
>>> sm.get_state_data("cart")
{'items': [], 'coupon': None}

```

Plain callables in `data` (like `list` above) are treated as **factories**: they are
called on every entry to produce a fresh value.

## DataVar

Use {class}`~statemachine.state_data.DataVar` for more control. It accepts a
`default` **or** a `factory`, plus an optional `type` that is enforced on assignment.

```py
>>> class Counter(StateChart):
...     counting = State(initial=True, data={
...         "count": DataVar(0, type=int),
...         "seen": DataVar(factory=set),
...     })
...     done = State(final=True)
...     stop = counting.to(done)

>>> sm = Counter()
>>> sm.set_state_data("counting", "count", 1)
>>> sm.set_state_data("counting", "count", "two")
Traceback (most recent call last):
...
statemachine.exceptions.InvalidDefinition: Invalid value 'two' for 'count': expected int, got str.

>>> DataVar(0, factory=int)
Traceback (most recent call last):
...
statemachine.exceptions.InvalidDefinition: DataVar cannot declare both 'default' and 'factory'.

```

`set_state_data()` also raises `InvalidDefinition` when the state
is not active or does not declare the key.

## Accessing data from callbacks

Callbacks can ask for `state_data`, alongside the other injected parameters such as
`source`, `target` or `event_data`. It is a live mapping: assignments are stored
back in the state that owns the key, so data persists across `on_enter` and
`on_exit` callbacks.

For transition callbacks, `state_data` is scoped to the transition's `state`:
the source for `before`/`on`, the target for `after`. For `on_enter_*` and
`on_exit_*`, it is scoped to the state being entered or exited.

### Hierarchical scoping

`state_data` merges the data of the state with the data of all its ancestors. On a
key collision, the child shadows its parent. Parallel regions are isolated: a region
never sees the data of its siblings.

```py
>>> class Editor(StateChart):
...     class session(State.Compound, data={"user": "ana", "mode": "view"}):
...         viewing = State(initial=True)
...         editing = State(data={"mode": "edit", "draft": ""})
...
...         edit = viewing.to(editing)
...
...         def on_enter_editing(self, state_data):
...             state_data["draft"] = f"{state_data['user']} is in {state_data['mode']} mode"

>>> sm = Editor()
>>> sm.send("edit")
>>> sm.get_state_data("editing")
{'mode': 'edit', 'draft': 'ana is in edit mode'}
>>> sm.get_state_data("session")
{'user': 'ana', 'mode': 'view'}

```

## History

When a {ref}`history state <history-states>` is recalled, the data saved when its parent was exited
is restored as well. A deep history restores the data of every descendant that was
active. A shallow history restores only the direct children; deeper states start
again from their defaults.

```py
>>> from statemachine import HistoryState

>>> class Wizard(StateChart):
...     class form(State.Compound):
...         step1 = State(initial=True, data={"name": ""})
...         step2 = State(data={"email": ""})
...         h = HistoryState()
...         next_step = step1.to(step2)
...     paused = State()
...
...     pause = form.to(paused)
...     resume = paused.to(form.h)

>>> sm = Wizard()
>>> sm.send("next_step")
>>> sm.set_state_data("step2", "email", "ana@example.com")
>>> sm.send("pause")
>>> sm.send("resume")
>>> sm.get_state_data("step2")
{'email': 'ana@example.com'}

```

## Tracking changes

`get_data_changes()` returns a {class}`~statemachine.state_data.DataChangeInfo` for
every assignment made through `set_state_data()` or `state_data` during the current
macrostep. The list is cleared when the next macrostep starts.

```py
>>> sm = Counter()
>>> sm.set_state_data("counting", "count", 3)
>>> [(c.state_id, c.key, c.old_value, c.new_value) for c in sm.get_data_changes()]
[('counting', 'count', 0, 3)]

>>> sm.send("stop")
>>> sm.get_data_changes()
[]

```

## Compound and parallel states

`State.Compound` and `State.Parallel` accept `data` as a class keyword:
`class session(State.Compound, data={...})`.

## SCXML

A state's own `<datamodel>` is also read as state data. Each `<data>` element with
`id` and `expr` attributes whose `expr` is a valid Python literal becomes a
state data variable.

## Diagrams

Diagrams list each state's data variables, e.g. `data / count: int = 0`.

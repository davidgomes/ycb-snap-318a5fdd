# Aspects

`createAspect(...constituents)` returns a distinct aspect over two or more traits.

## Creation

- At least two constituents.
- Nested aspects flatten to their traits in first-seen order.
- Tag traits are valid and contribute no schema fields.
- The same field name on two constituents throws.
- Relation values and relation traits throw.
- AoS traits throw.
- Public fields: `id`, `traits`, `schema`.
- Calling an aspect with initial data returns `[aspect, data]`, same shape as a trait tuple.

## Entity operations

| Operation | Behavior |
| --------- | -------- |
| `has` | True only when every constituent is present. |
| `get` | Merged field object, or `undefined` if any constituent is missing. |
| `set` | Writes each field to the trait that owns it and runs that trait's change detection. |
| `add` | Adds only missing constituents. Initial values are split by field. Existing constituents are left as they are. |
| `remove` | Removes every constituent. |

## Queries

An aspect parameter requires every constituent (logical AND).

- `readEach` passes one merged object for the aspect.
- `updateEach` splits that object back onto constituent stores.
- `Not(aspect)` matches entities missing at least one constituent.
- `Or(aspect, …)` matches when every constituent of the aspect is present, or another term matches.
- `Changed(aspect)` matches when any constituent's data changed and all constituents are present.
- `Added(aspect)` matches the transition into all constituents being present.
- `Removed(aspect)` matches the transition out of that complete set.
- Aspects combine with other query parameters and modifiers.

## Subscriptions

- `onAdd` fires when the entity moves from missing at least one constituent to having all of them.
- `onRemove` fires on the reverse transition.
- `onChange` fires when any constituent changes while all constituents are present.

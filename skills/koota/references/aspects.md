# Aspects

`createAspect` groups two or more traits so entity operations and queries treat them as one unit.

```typescript
import { createAspect, trait, Not, createAdded, createChanged, createRemoved } from 'koota'

const Position = trait({ x: 0, y: 0 })
const Velocity = trait({ vx: 0, vy: 0 })
const IsPlayer = trait()

const Movement = createAspect(Position, Velocity, IsPlayer)
```

## Creation

- Pass two or more traits. A single argument throws.
- Each call returns a distinct aspect with its own `id`.
- Nested aspects flatten to their constituent traits.
- `traits` is the flattened list. `schema` merges constituent defaults. Tag traits add no fields.
- Overlapping field names throw at creation.
- Relation constituents and AoS (callback) traits throw. Tag traits are valid.

## Entity operations

- `has` is true only when every constituent is present.
- `get` returns one merged object, or `undefined` when any constituent is missing.
- `set` writes each field onto the trait that owns it and runs that trait's change detection.
- `add` adds only constituents the entity does not already have. Initial values are split by field. Existing constituents are left unchanged.
- `remove` removes every constituent.

`spawn`, `add`, and `init` accept an aspect or `[aspect, values]`.

## Queries and events

An aspect parameter requires every constituent.

- `readEach` yields one merged object.
- `updateEach` writes fields back to the constituent stores.
- `Not(aspect)` matches entities missing at least one constituent.
- `Changed(aspect)` matches when any constituent's data changed while all constituents are present.
- `Added(aspect)` matches the transition to all constituents present.
- `Removed(aspect)` matches the transition away from all constituents present.
- Aspects work inside `Or` the same way: the whole group is present, or it is not.
- `onAdd` fires when an entity goes from incomplete to complete.
- `onRemove` fires on the reverse transition.
- `onChange` fires when any constituent changes while all constituents are present.

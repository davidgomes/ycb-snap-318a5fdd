# Aspects

An aspect is a distinct group of two or more traits. Use one when a system always reads or writes those traits together.

```typescript
import { createAspect, trait } from 'koota'

const Health = trait({ hp: 10 })
const Shield = trait({ sp: 5 })
const IsDead = trait()
const Combat = createAspect(Health, Shield, IsDead)
```

## Creation rules

- Two or more traits are required. A single nested aspect is enough when it already flattens to two or more traits.
- Nested aspects flatten. A trait that appears more than once is kept once, in first-seen order.
- Overlapping field names throw at creation time.
- Relation constituents throw. Tag traits are valid and contribute no fields.
- Each call returns a new aspect with its own `id`, even for the same constituents.
- `traits` is the flattened list. `schema` merges the constituent schemas.

## Entity operations

```typescript
entity.add(Combat({ hp: 8, sp: 2 }))
entity.has(Combat) // true when Health, Shield, and IsDead are all present
entity.get(Combat) // { hp: 8, sp: 2 }, or undefined if any constituent is missing
entity.set(Combat, { hp: 4 }) // writes hp onto Health; Shield is left alone
entity.set(Combat, (prev) => ({ hp: prev.hp - 1 }))
entity.remove(Combat) // removes every constituent
```

`add` installs only constituents the entity does not already have. Initial values are split by field and sent to the owning trait. `set` does the same split and runs each written trait's change detection. Tag constituents are added and removed with the group and do not appear in the merged record.

`world.add`, `world.remove`, `world.has`, `world.get`, and `world.set` accept aspects for the world entity. `spawn` accepts an aspect or `Aspect({ ...initial })`.

## Queries

An aspect query parameter requires every constituent.

```typescript
world.query(Combat).updateEach(([combat]) => {
  combat.hp -= 1
  combat.sp += 1
})

world.query(Combat, Name).readEach(([combat, name]) => {
  // combat is { hp, sp }; name is the Name record
})
```

`readEach` copies constituent fields into one object. `updateEach` writes those fields back to the owning stores. Tags are omitted from the column, the same way a tag query parameter is omitted.

Modifiers:

| Modifier           | Match                                                |
| ------------------ | ---------------------------------------------------- |
| `Not(Combat)`      | Missing at least one constituent                     |
| `Or(Combat, Name)` | Complete combat aspect, or `Name`                    |
| `Added(Combat)`    | Transition to all constituents present               |
| `Removed(Combat)`  | Transition away from all constituents present        |
| `Changed(Combat)`  | Any constituent's data changed while all are present |

Entities that already had every constituent before the aspect was created are treated as already present, so a later `Added` cursor does not report them.

## Observers

```typescript
world.onAdd(Combat, (entity) => {
  // entity.has(Combat) is true and get() returns the merged record
})
world.onRemove(Combat, (entity) => {
  // constituents are still readable; at least one is about to go away
})
world.onChange(Combat, (entity) => {
  // any constituent changed while the entity was complete
})
```

`onAdd` fires on the transition from incomplete to complete. `onRemove` fires on the reverse transition, once, even if further constituents are removed afterward. `onChange` does not fire for an incomplete entity.

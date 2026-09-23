# Snapshots

Copy entity state out of a world, diff it, and restore it. Every snapshot is scoped by a trait registry so unknown traits and relations fail loudly instead of being dropped.

## Contents

- [Registry](#registry)
- [Entity snapshots](#entity-snapshots)
- [World snapshots](#world-snapshots)
- [Rollback](#rollback)
- [Diffs](#diffs)

## Registry

```typescript
import { createTraitRegistry, trait, relation } from 'koota'

const Position = trait({ x: 0, y: 0 })
const IsPlayer = trait()
const ChildOf = relation({ exclusive: true })
const Contains = relation({ store: { amount: 0 } })

const registry = createTraitRegistry(
  ['Position', Position],
  ['IsPlayer', IsPlayer],
  ['ChildOf', ChildOf],
  ['Contains', Contains]
)
```

`createTraitRegistry(...entries)` takes `[string, Trait | Relation]` tuples. It throws if a key, trait, or relation is registered twice.

## Entity snapshots

```typescript
const snapshot = entity.snapshot(registry)
// or snapshotEntity(world, entity, registry)
```

```ts
type EntitySnapshot = {
  id: number // entity.id(), not the packed entity number
  traits: Record<string, object | true>
  relations?: Record<string, Array<{ targetId: number; data?: object }>>
}
```

- Tag traits are `true`.
- Data traits are deep copies. Mutating the snapshot does not mutate the world.
- Relations with a `store` include `data` as a deep copy. Tag relations omit `data`.
- `relations` is omitted entirely when the entity has no relations.
- `targetId` is `target.id()`.
- Throws if the entity is destroyed.
- Throws if the entity has a trait or relation that is not in the registry.
- The internal `IsExcluded` tag is ignored unless it is registered.

## World snapshots

```typescript
const checkpoint = world.snapshot(registry)
// { entities: EntitySnapshot[] }
```

The internal world entity is excluded. Entities are sorted by `entity.id()` ascending. World traits are not part of the checkpoint.

## Rollback

`entity.rollback(registry, snapshot)` removes traits and relations the entity currently has that are not in the snapshot, then adds or updates the rest so the entity matches. It throws if:

- the entity is destroyed
- the snapshot contains a key the registry does not name
- a relation target id is not alive in the world

`world.rollback(registry, checkpoint)` resets the world and recreates every checkpoint entity at the same `entity.id()`. Generation is 0, so entity references that were never recycled stay valid. It throws if a key is unknown or a relation target is neither a checkpoint entity nor the world entity. On those errors the world is left unchanged.

The world entity (id `0`) is a valid relation target even though it is not in `checkpoint.entities`.

## Diffs

```typescript
diffEntitySnapshots(a, b)
// { addedTraits: string[], removedTraits: string[], changedTraits: string[] }

diffWorldSnapshots(before, after)
// { added: number[], removed: number[], changed: number[] }
```

All arrays are sorted ascending. Trait and relation data use shallow equality, so nested objects compare by reference. Trait key order, relation key order, and relation target order do not affect world equality. An entity with `relations: {}` matches one with no `relations` key.

`diffEntitySnapshots` compares the `traits` record only. `diffWorldSnapshots` also compares relations. Either diff throws if an argument is null or undefined. The world diff also throws when `entities` is not an array.

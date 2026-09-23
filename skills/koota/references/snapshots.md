# Snapshots and Rollback

Capture entity or world state as plain data and restore it later (undo, save/load, netcode rollback).

## Registry

Snapshots key traits and relations by string. Register everything that will be snapshotted:

```typescript
import { createTraitRegistry } from 'koota'

const registry = createTraitRegistry(
  ['position', Position],
  ['isPlayer', IsPlayer],
  ['childOf', ChildOf]
)
```

Duplicate keys, traits, or relations throw.

## Entity snapshots

```typescript
const snapshot = entity.snapshot(registry) // or snapshotEntity(world, entity, registry)
// { id: number, traits: { position: { x, y }, isPlayer: true }, relations?: { childOf: [{ targetId, data? }] } }

entity.rollback(registry, snapshot) // or rollbackEntity(world, entity, registry, snapshot)
```

- Tags are `true`, data traits and relation store data are deep copies.
- `relations` is omitted when the entity has none.
- `id` and `targetId` are `entity.id()` values.
- Rollback removes traits and relations not in the snapshot, then adds or updates the rest. Relation targets must be alive.
- Unregistered traits, unknown keys, and destroyed entities throw.

## World checkpoints

```typescript
const checkpoint = world.snapshot(registry) // { entities: EntitySnapshot[] }, excludes the world entity
world.rollback(registry, checkpoint) // destroys all entities and recreates them with the checkpoint IDs
```

Old entity handles become stale after a world rollback. Query the world again to get the restored entities.

## Diffing

```typescript
diffEntitySnapshots(a, b) // { addedTraits, removedTraits, changedTraits }
diffWorldSnapshots(before, after) // { added, removed, changed } entity IDs
```

Results are sorted ascending. Data is compared shallowly; key and relation target order are ignored.

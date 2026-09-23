---
title: Snapshot
description: Entity and world snapshots
nav: 8
---

Snapshots copy entity state so it can be diffed or restored. A registry names every trait and relation the snapshot is allowed to read or write.

```js
const registry = createTraitRegistry(
  ['Position', Position],
  ['IsPlayer', IsPlayer],
  ['ChildOf', ChildOf],
  ['Contains', Contains]
)

// Entity
const snapshot = entity.snapshot(registry)
entity.rollback(registry, snapshot)

// World, excluding the internal world entity
const checkpoint = world.snapshot(registry)
world.rollback(registry, checkpoint)
```

The same functions are exported as `snapshotEntity`, `rollbackEntity`, `snapshotWorld`, and `rollbackWorld`.

## Registry

`createTraitRegistry(...entries)` accepts `[string, Trait | Relation]` tuples. It throws if a key, trait, or relation identity is repeated.

## Entity snapshot

```ts
{
  id: number // entity.id()
  traits: Record<string, object | true>
  relations?: Record<string, Array<{ targetId: number, data?: object }>>
}
```

Tag traits are stored as `true`. Data traits are deep copies. Relations with a store include `data` as a deep copy. `relations` is left off entirely when the entity has no relations. Targets use `entity.id()`.

Throws if the entity is destroyed or has a trait or relation that is not in the registry.

## World snapshot

`{ entities: EntitySnapshot[] }`, sorted by entity id, excluding the internal world entity.

## Rollback

`rollbackEntity` removes current traits and relations that are not in the snapshot, then adds or updates the rest to match. Throws if the entity is destroyed, a snapshot key is unknown, or a relation target is not alive.

`rollbackWorld` replaces world state and recreates entities at the same ids. Throws if a key is unknown or a relation target is not in the checkpoint and is not the world entity. A failed rollback leaves the world unchanged.

## Diff

```js
diffEntitySnapshots(a, b)
// { addedTraits, removedTraits, changedTraits } sorted ascending

diffWorldSnapshots(before, after)
// { added, removed, changed } entity ids, sorted ascending
```

Trait and relation data are compared shallowly. Key order and relation target order do not matter. `relations: {}` is the same as omitting `relations`. Null or undefined arguments throw. The world diff also throws when `entities` is not an array.

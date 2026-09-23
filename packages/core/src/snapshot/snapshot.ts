import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { getRelationData, getRelationTargets } from '../relation/relation';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import type { Trait } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world/types';
import type {
    EntitySnapshot,
    EntitySnapshotDiff,
    RelationSnapshotLink,
    TraitRegistry,
    TraitRegistryEntry,
    WorldSnapshot,
    WorldSnapshotDiff,
} from './types';

const $traitRegistry = Symbol.for('koota.traitRegistry');

type RegistryData = {
    byKey: Map<string, TraitRegistryEntry>;
    traitKeys: Map<Trait, string>;
    relationKeys: Map<Relation<Trait>, string>;
};

type RegistryInternal = TraitRegistry & { [$traitRegistry]: RegistryData };

function deepCopy<T>(value: T, seen = new WeakMap<object, unknown>()): T {
    if (typeof value !== 'object' || value === null) return value;

    const cached = seen.get(value);
    if (cached) return cached as T;

    if (Array.isArray(value)) {
        const copy: unknown[] = [];
        seen.set(value, copy);
        for (const item of value) copy.push(deepCopy(item, seen));
        return copy as T;
    }

    const copy = Object.create(Object.getPrototypeOf(value));
    seen.set(value, copy);
    for (const key of Object.keys(value)) {
        copy[key] = deepCopy((value as Record<string, unknown>)[key], seen);
    }
    return copy as T;
}

function getRegistry(registry: TraitRegistry): RegistryData {
    const data = (registry as RegistryInternal)[$traitRegistry];
    if (!data) throw new Error('Koota: Expected a trait registry.');
    return data;
}

function indexAlive(world: World): Map<number, Entity> {
    const alive = new Map<number, Entity>();
    for (const entity of world.entities) alive.set(getEntityId(entity), entity);
    return alive;
}

function relationHasStore(relation: Relation<Trait>): boolean {
    return relation[$internal].trait[$internal].type !== 'tag';
}

/**
 * Register traits and relations under stable string keys.
 * Throws if a key, trait, or relation is registered more than once.
 */
export function createTraitRegistry(...entries: Array<[string, TraitRegistryEntry]>): TraitRegistry {
    const byKey = new Map<string, TraitRegistryEntry>();
    const traitKeys = new Map<Trait, string>();
    const relationKeys = new Map<Relation<Trait>, string>();

    for (const [key, value] of entries) {
        if (byKey.has(key)) throw new Error('Koota: Duplicate key.');

        if (isRelation(value)) {
            if (relationKeys.has(value)) throw new Error('Koota: Duplicate relation in registry.');
            relationKeys.set(value, key);
        } else {
            if (typeof value !== 'function') {
                throw new Error('Koota: Registry values must be traits or relations.');
            }
            if (traitKeys.has(value)) throw new Error('Koota: Duplicate trait in registry.');
            traitKeys.set(value, key);
        }

        byKey.set(key, value);
    }

    const registry = {
        [$traitRegistry]: { byKey, traitKeys, relationKeys },
        get(key: string) {
            return byKey.get(key);
        },
        has(key: string) {
            return byKey.has(key);
        },
        keys() {
            return [...byKey.keys()];
        },
    };

    return registry as TraitRegistry;
}

function readTraitValue(world: World, entity: Entity, trait: Trait): object | true {
    if (trait[$internal].type === 'tag') return true;
    const value = getTrait(world, entity, trait);
    return deepCopy(value ?? {}) as object;
}

function readRelationLinks(
    world: World,
    entity: Entity,
    relation: Relation<Trait>
): RelationSnapshotLink[] {
    const hasStore = relationHasStore(relation);
    const links: RelationSnapshotLink[] = [];

    for (const target of getRelationTargets(world, relation, entity)) {
        const link: RelationSnapshotLink = { targetId: getEntityId(target) };
        if (hasStore) {
            const data = getRelationData(world, entity, relation, target);
            link.data = deepCopy((data ?? {}) as object);
        }
        links.push(link);
    }

    return links;
}

export function snapshotEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry
): EntitySnapshot {
    if (!world.has(entity)) throw new Error('Koota: Cannot snapshot a destroyed entity.');

    const data = getRegistry(registry);
    const current = world[$internal].entityTraits.get(entity);
    if (!current) throw new Error('Koota: Cannot snapshot a destroyed entity.');

    const traits: Record<string, object | true> = {};
    const relations: Record<string, RelationSnapshotLink[]> = {};

    for (const trait of current) {
        const relation = trait[$internal].relation;
        if (relation) {
            const key = data.relationKeys.get(relation);
            if (key === undefined) throw new Error('Koota: Unregistered relation.');
            const links = readRelationLinks(world, entity, relation);
            if (links.length > 0) relations[key] = links;
            continue;
        }

        const key = data.traitKeys.get(trait);
        if (key === undefined) throw new Error('Koota: Unregistered trait.');
        traits[key] = readTraitValue(world, entity, trait);
    }

    const snapshot: EntitySnapshot = { id: getEntityId(entity), traits };
    if (Object.keys(relations).length > 0) snapshot.relations = relations;
    return snapshot;
}

export function snapshotWorld(world: World, registry: TraitRegistry): WorldSnapshot {
    const worldEntity = world[$internal].worldEntity;
    const entities: EntitySnapshot[] = [];

    for (const entity of world.entities) {
        if (entity === worldEntity) continue;
        entities.push(snapshotEntity(world, entity, registry));
    }

    return { entities };
}

function assertKnownKeys(data: RegistryData, snapshot: EntitySnapshot) {
    for (const key of Object.keys(snapshot.traits ?? {})) {
        const entry = data.byKey.get(key);
        if (!entry || isRelation(entry)) throw new Error('Koota: Unknown registry key.');
    }

    for (const key of Object.keys(snapshot.relations ?? {})) {
        const entry = data.byKey.get(key);
        if (!entry || !isRelation(entry)) throw new Error('Koota: Unknown registry key.');
    }
}

function assertTargetsAlive(snapshot: EntitySnapshot, alive: Map<number, Entity>) {
    const relations = snapshot.relations;
    if (!relations) return;

    for (const links of Object.values(relations)) {
        for (const link of links) {
            const target = alive.get(link.targetId);
            if (target === undefined)
                throw new Error('Koota: Relation target entity does not exist.');
        }
    }
}

function applyEntitySnapshot(
    world: World,
    entity: Entity,
    data: RegistryData,
    snapshot: EntitySnapshot,
    alive: Map<number, Entity>
) {
    const desiredTraits = new Set<Trait>();
    for (const key of Object.keys(snapshot.traits ?? {})) {
        desiredTraits.add(data.byKey.get(key) as Trait);
    }

    const desiredRelations = new Set<Relation<Trait>>();
    for (const key of Object.keys(snapshot.relations ?? {})) {
        desiredRelations.add(data.byKey.get(key) as Relation<Trait>);
    }

    const current = [...(world[$internal].entityTraits.get(entity) ?? [])];
    for (const trait of current) {
        const relation = trait[$internal].relation;
        if (relation) {
            if (!desiredRelations.has(relation)) removeTrait(world, entity, trait);
        } else if (!desiredTraits.has(trait)) {
            removeTrait(world, entity, trait);
        }
    }

    for (const [key, value] of Object.entries(snapshot.traits ?? {})) {
        const trait = data.byKey.get(key) as Trait;
        if (trait[$internal].type === 'tag' || value === true) {
            if (!hasTrait(world, entity, trait)) addTrait(world, entity, trait);
            continue;
        }

        const copied = deepCopy(value);
        if (!hasTrait(world, entity, trait)) addTrait(world, entity, [trait, copied]);
        else if (!shallowEqual(getTrait(world, entity, trait), copied)) {
            setTrait(world, entity, trait, copied, true);
        }
    }

    for (const [key, links] of Object.entries(snapshot.relations ?? {})) {
        const relation = data.byKey.get(key) as Relation<Trait>;
        const hasStore = relationHasStore(relation);
        const currentTargets = getRelationTargets(world, relation, entity);
        const currentIds = currentTargets.map((target) => getEntityId(target));
        const sameTargets =
            currentIds.length === links.length &&
            currentIds.every((id, index) => id === links[index].targetId);

        if (!sameTargets) {
            for (const target of currentTargets) removeTrait(world, entity, relation(target));
        }

        for (const link of links) {
            const target = alive.get(link.targetId);
            if (target === undefined || !world.has(target)) {
                throw new Error('Koota: Relation target entity does not exist.');
            }

            const copied =
                hasStore && link.data !== undefined
                    ? (deepCopy(link.data) as Record<string, unknown>)
                    : undefined;

            if (!sameTargets) {
                if (copied) addTrait(world, entity, relation(target, copied));
                else addTrait(world, entity, relation(target));
                continue;
            }

            if (copied && !shallowEqual(getRelationData(world, entity, relation, target), copied)) {
                setTrait(world, entity, relation(target), copied, true);
            }
        }
    }
}

export function rollbackEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
) {
    if (!world.has(entity)) throw new Error('Koota: Cannot rollback a destroyed entity.');
    if (snapshot == null) throw new Error('Koota: Cannot rollback a null or undefined snapshot.');

    const data = getRegistry(registry);
    assertKnownKeys(data, snapshot);
    const alive = indexAlive(world);
    assertTargetsAlive(snapshot, alive);
    applyEntitySnapshot(world, entity, data, snapshot, alive);
}

/**
 * Spawn `id` from a fresh entity index by advancing the allocator.
 * Holes are left unallocated so checkpoint ids are preserved without placeholder entities.
 */
function spawnWithId(world: World, id: number): Entity {
    const index = world[$internal].entityIndex;
    if (id < index.maxId) throw new Error('Koota: Cannot recreate entity id.');
    index.maxId = id;
    const entity = world.spawn();
    if (getEntityId(entity) !== id) throw new Error('Koota: Cannot recreate entity id.');
    return entity;
}

export function rollbackWorld(world: World, registry: TraitRegistry, checkpoint: WorldSnapshot) {
    if (checkpoint == null || !Array.isArray(checkpoint.entities)) {
        throw new Error('Koota: World snapshot must include an entities array.');
    }

    const data = getRegistry(registry);
    const snapshots = new Map<number, EntitySnapshot>();
    for (const snapshot of checkpoint.entities) snapshots.set(snapshot.id, snapshot);

    for (const snapshot of snapshots.values()) assertKnownKeys(data, snapshot);

    const worldEntity = world[$internal].worldEntity;
    if (worldEntity == null || !world.has(worldEntity)) {
        throw new Error('Koota: World is not initialized.');
    }
    const worldEntityId = getEntityId(worldEntity);

    for (const snapshot of snapshots.values()) {
        const relations = snapshot.relations;
        if (!relations) continue;
        for (const links of Object.values(relations)) {
            for (const link of links) {
                if (link.targetId !== worldEntityId && !snapshots.has(link.targetId)) {
                    throw new Error('Koota: Relation target entity does not exist.');
                }
            }
        }
    }

    world.reset();

    const alive = new Map<number, Entity>();
    const resetWorldEntity = world[$internal].worldEntity;
    alive.set(getEntityId(resetWorldEntity), resetWorldEntity);

    const ids = [...snapshots.keys()]
        .filter((id) => id !== getEntityId(resetWorldEntity))
        .sort((a, b) => a - b);

    for (const id of ids) alive.set(id, spawnWithId(world, id));

    for (const [id, snapshot] of snapshots) {
        const entity = alive.get(id);
        if (entity === undefined) throw new Error('Koota: Relation target entity does not exist.');
        applyEntitySnapshot(world, entity, data, snapshot, alive);
    }
}

function traitValueEqual(a: object | true, b: object | true): boolean {
    if (a === true || b === true) return a === b;
    return shallowEqual(a, b);
}

function traitsEqual(
    a: Record<string, object | true> | undefined,
    b: Record<string, object | true> | undefined
): boolean {
    const left = a ?? {};
    const right = b ?? {};
    const keys = Object.keys(left);
    if (keys.length !== Object.keys(right).length) return false;

    for (const key of keys) {
        if (!Object.prototype.hasOwnProperty.call(right, key)) return false;
        if (!traitValueEqual(left[key], right[key])) return false;
    }

    return true;
}

function linksEqual(
    a: RelationSnapshotLink[] | undefined,
    b: RelationSnapshotLink[] | undefined
): boolean {
    const left = [...(a ?? [])].sort((x, y) => x.targetId - y.targetId);
    const right = [...(b ?? [])].sort((x, y) => x.targetId - y.targetId);
    if (left.length !== right.length) return false;

    for (let i = 0; i < left.length; i++) {
        if (left[i].targetId !== right[i].targetId) return false;
        const leftData = left[i].data;
        const rightData = right[i].data;
        if (leftData == null && rightData == null) continue;
        if (!shallowEqual(leftData, rightData)) return false;
    }

    return true;
}

function relationsEqual(a: EntitySnapshot['relations'], b: EntitySnapshot['relations']): boolean {
    const leftEmpty = a == null || Object.keys(a).length === 0;
    const rightEmpty = b == null || Object.keys(b).length === 0;
    if (leftEmpty && rightEmpty) return true;
    if (leftEmpty || rightEmpty || !a || !b) return false;

    const keys = Object.keys(a);
    if (keys.length !== Object.keys(b).length) return false;

    for (const key of keys) {
        if (!Object.prototype.hasOwnProperty.call(b, key)) return false;
        if (!linksEqual(a[key], b[key])) return false;
    }

    return true;
}

function entitySnapshotsEqual(a: EntitySnapshot, b: EntitySnapshot): boolean {
    return traitsEqual(a.traits, b.traits) && relationsEqual(a.relations, b.relations);
}

export function diffEntitySnapshots(a: EntitySnapshot, b: EntitySnapshot): EntitySnapshotDiff {
    if (a == null || b == null) throw new Error('Koota: Cannot diff null or undefined snapshots.');

    const left = a.traits ?? {};
    const right = b.traits ?? {};
    const addedTraits: string[] = [];
    const removedTraits: string[] = [];
    const changedTraits: string[] = [];

    for (const key of new Set([...Object.keys(left), ...Object.keys(right)])) {
        const inLeft = Object.prototype.hasOwnProperty.call(left, key);
        const inRight = Object.prototype.hasOwnProperty.call(right, key);
        if (inLeft && !inRight) removedTraits.push(key);
        else if (!inLeft && inRight) addedTraits.push(key);
        else if (!traitValueEqual(left[key], right[key])) changedTraits.push(key);
    }

    addedTraits.sort();
    removedTraits.sort();
    changedTraits.sort();

    return { addedTraits, removedTraits, changedTraits };
}

export function diffWorldSnapshots(before: WorldSnapshot, after: WorldSnapshot): WorldSnapshotDiff {
    if (
        before == null ||
        after == null ||
        !Array.isArray(before.entities) ||
        !Array.isArray(after.entities)
    ) {
        throw new Error('Koota: World snapshot must include an entities array.');
    }

    const previous = new Map<number, EntitySnapshot>();
    const next = new Map<number, EntitySnapshot>();
    for (const snapshot of before.entities) previous.set(snapshot.id, snapshot);
    for (const snapshot of after.entities) next.set(snapshot.id, snapshot);

    const added: number[] = [];
    const removed: number[] = [];
    const changed: number[] = [];

    for (const id of previous.keys()) {
        if (!next.has(id)) removed.push(id);
        else if (!entitySnapshotsEqual(previous.get(id)!, next.get(id)!)) changed.push(id);
    }

    for (const id of next.keys()) {
        if (!previous.has(id)) added.push(id);
    }

    added.sort((x, y) => x - y);
    removed.sort((x, y) => x - y);
    changed.sort((x, y) => x - y);

    return { added, removed, changed };
}

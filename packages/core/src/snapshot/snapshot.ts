import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { isEntityAlive } from '../entity/utils/entity-index';
import { getEntityId } from '../entity/utils/pack-entity';
import { IsExcluded } from '../query/query';
import { getRelationData, getRelationTargets, hasRelationToTarget } from '../relation/relation';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import type { Trait } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world/types';
import type {
    EntitySnapshot,
    EntitySnapshotDiff,
    RelationLinkSnapshot,
    TraitRegistry,
    WorldSnapshot,
    WorldSnapshotDiff,
} from './types';

type RegistryEntry = Trait | Relation<Trait>;
type RegistryInternal = TraitRegistry[typeof $internal];

function fail(message: string): never {
    throw new Error(`Koota: ${message}`);
}

function registryInternal(registry: TraitRegistry): RegistryInternal {
    const internal = registry?.[$internal];
    if (internal?.byKey == null || internal.keysByTrait == null || internal.keysByRelation == null) {
        fail('Invalid trait registry.');
    }
    return internal;
}

function deepCopy<T>(value: T, seen = new WeakMap<object, unknown>()): T {
    if (typeof value !== 'object' || value === null) return value;
    const cached = seen.get(value);
    if (cached) return cached as T;

    if (value instanceof Date) return new Date(value.getTime()) as T;
    if (value instanceof RegExp) return new RegExp(value.source, value.flags) as T;

    if (value instanceof Map) {
        const copy = new Map();
        seen.set(value, copy);
        for (const [key, nested] of value) {
            copy.set(deepCopy(key, seen), deepCopy(nested, seen));
        }
        return copy as T;
    }

    if (value instanceof Set) {
        const copy = new Set();
        seen.set(value, copy);
        for (const nested of value) copy.add(deepCopy(nested, seen));
        return copy as T;
    }

    if (Array.isArray(value)) {
        const copy: unknown[] = [];
        seen.set(value, copy);
        for (let i = 0; i < value.length; i++) copy[i] = deepCopy(value[i], seen);
        return copy as T;
    }

    const copy = Object.create(Object.getPrototypeOf(value));
    seen.set(value, copy);
    for (const key of Object.keys(value)) {
        copy[key] = deepCopy((value as Record<string, unknown>)[key], seen);
    }
    return copy;
}

function assertEntityAlive(world: World, entity: Entity, action: 'snapshot' | 'rollback'): void {
    if (!isEntityAlive(world[$internal].entityIndex, entity)) {
        fail(
            action === 'snapshot'
                ? 'Cannot snapshot a destroyed entity.'
                : 'Cannot rollback a destroyed entity.'
        );
    }
}

function getAliveEntityById(world: World, id: number): Entity | undefined {
    if (typeof id !== 'number' || !Number.isInteger(id) || id < 0) return undefined;
    const index = world[$internal].entityIndex;
    const denseIndex = index.sparse[id];
    if (denseIndex === undefined || denseIndex >= index.aliveCount) return undefined;
    const entity = index.dense[denseIndex];
    if (getEntityId(entity) !== id) return undefined;
    return entity;
}

function relationStoresData(relation: Relation<Trait>): boolean {
    return relation[$internal].trait[$internal].type !== 'tag';
}

function traitRecord(snapshot: EntitySnapshot | null | undefined): Record<string, object | true> {
    return snapshot?.traits ?? {};
}

function relationRecord(
    snapshot: EntitySnapshot | null | undefined
): Record<string, RelationLinkSnapshot[]> {
    return snapshot?.relations ?? {};
}

function assertKnownTraitKey(registry: RegistryInternal, key: string): Trait {
    const entry = registry.byKey.get(key) as RegistryEntry | undefined;
    if (entry == null || isRelation(entry)) fail(`Unknown trait registry key "${key}".`);
    return entry;
}

function assertKnownRelationKey(registry: RegistryInternal, key: string): Relation<Trait> {
    const entry = registry.byKey.get(key) as RegistryEntry | undefined;
    if (entry == null || !isRelation(entry)) fail(`Unknown trait registry key "${key}".`);
    return entry;
}

function linkDataEqual(a: object | undefined, b: object | undefined): boolean {
    if (a === undefined || b === undefined) return a === b;
    return shallowEqual(a, b);
}

function linksEqual(a: RelationLinkSnapshot[], b: RelationLinkSnapshot[]): boolean {
    if (a.length !== b.length) return false;
    const used = Array.from({ length: b.length }, () => false);

    for (let i = 0; i < a.length; i++) {
        let found = false;
        for (let j = 0; j < b.length; j++) {
            if (used[j]) continue;
            if (a[i].targetId !== b[j].targetId) continue;
            if (!linkDataEqual(a[i].data, b[j].data)) continue;
            used[j] = true;
            found = true;
            break;
        }
        if (!found) return false;
    }

    return true;
}

function traitsEqual(
    a: Record<string, object | true> | undefined,
    b: Record<string, object | true> | undefined
): boolean {
    const aTraits = a ?? {};
    const bTraits = b ?? {};
    const aKeys = Object.keys(aTraits);
    if (aKeys.length !== Object.keys(bTraits).length) return false;

    for (const key of aKeys) {
        if (!Object.prototype.hasOwnProperty.call(bTraits, key)) return false;
        if (!shallowEqual(aTraits[key], bTraits[key])) return false;
    }

    return true;
}

function relationsEqual(a: EntitySnapshot['relations'], b: EntitySnapshot['relations']): boolean {
    const aRelations = a ?? {};
    const bRelations = b ?? {};
    const aKeys = Object.keys(aRelations);
    if (aKeys.length !== Object.keys(bRelations).length) return false;

    for (const key of aKeys) {
        if (!Object.prototype.hasOwnProperty.call(bRelations, key)) return false;
        if (!linksEqual(aRelations[key] ?? [], bRelations[key] ?? [])) return false;
    }

    return true;
}

function entityContentEqual(a: EntitySnapshot, b: EntitySnapshot): boolean {
    return traitsEqual(a.traits, b.traits) && relationsEqual(a.relations, b.relations);
}

function sortStrings(values: string[]): string[] {
    values.sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
    return values;
}

function sortNumbers(values: number[]): number[] {
    values.sort((a, b) => a - b);
    return values;
}

/**
 * Names the traits and relations that snapshots are allowed to touch.
 * Throws if a key, trait, or relation is registered more than once.
 */
export function createTraitRegistry(
    ...entries: Array<[string, Trait | Relation<Trait>]>
): TraitRegistry {
    const byKey = new Map<string, RegistryEntry>();
    const keysByTrait = new Map<Trait, string>();
    const keysByRelation = new Map<Relation<Trait>, string>();

    for (const [key, value] of entries) {
        if (typeof key !== 'string') fail('Trait registry keys must be strings.');
        if (byKey.has(key)) fail(`Duplicate key "${key}" in trait registry.`);

        if (isRelation(value)) {
            if (keysByRelation.has(value)) fail('Duplicate relation in trait registry.');
            keysByRelation.set(value, key);
        } else {
            if (keysByTrait.has(value as Trait)) fail('Duplicate trait in trait registry.');
            keysByTrait.set(value as Trait, key);
        }

        byKey.set(key, value);
    }

    return {
        [$internal]: {
            byKey,
            keysByTrait,
            keysByRelation,
        },
    };
}

/**
 * Deep-copies one entity's registered traits and relations.
 * Tag traits are stored as `true`. Relation payloads are included only when the
 * relation has a store. `relations` is omitted when the entity has no relations.
 */
export function snapshotEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry
): EntitySnapshot {
    assertEntityAlive(world, entity, 'snapshot');
    const internal = registryInternal(registry);
    const current = world[$internal].entityTraits.get(entity);
    const traits: Record<string, object | true> = {};
    const relations: Record<string, RelationLinkSnapshot[]> = {};
    let hasRelations = false;

    if (current) {
        for (const trait of current) {
            const relation = trait[$internal].relation;
            if (relation) {
                const key = internal.keysByRelation.get(relation);
                if (key == null) fail('Cannot snapshot an unregistered trait or relation.');

                const links: RelationLinkSnapshot[] = [];
                const storeData = relationStoresData(relation);
                for (const target of getRelationTargets(world, relation, entity)) {
                    const link: RelationLinkSnapshot = { targetId: getEntityId(target) };
                    if (storeData) {
                        const data = getRelationData(world, entity, relation, target);
                        if (data !== undefined) link.data = deepCopy(data) as object;
                    }
                    links.push(link);
                }

                if (links.length > 0) {
                    relations[key] = links;
                    hasRelations = true;
                }
                continue;
            }

            const key = internal.keysByTrait.get(trait);
            if (key == null) fail('Cannot snapshot an unregistered trait or relation.');

            if (trait[$internal].type === 'tag') {
                traits[key] = true;
            } else {
                traits[key] = deepCopy(getTrait(world, entity, trait) ?? {}) as object;
            }
        }
    }

    const snapshot: EntitySnapshot = { id: getEntityId(entity), traits };
    if (hasRelations) snapshot.relations = relations;
    return snapshot;
}

/** Snapshots every alive entity except the internal world entity. */
export function snapshotWorld(world: World, registry: TraitRegistry): WorldSnapshot {
    const worldEntity = world[$internal].worldEntity;
    const entities: EntitySnapshot[] = [];
    for (const entity of world.entities) {
        if (entity === worldEntity) continue;
        entities.push(snapshotEntity(world, entity, registry));
    }
    return { entities };
}

function assertSnapshotKeys(registry: RegistryInternal, snapshot: EntitySnapshot): void {
    const traits = traitRecord(snapshot);
    const relations = relationRecord(snapshot);

    for (const key of Object.keys(traits)) assertKnownTraitKey(registry, key);
    for (const key of Object.keys(relations)) assertKnownRelationKey(registry, key);
}

function assertRelationTargetsExist(world: World, snapshot: EntitySnapshot): void {
    const relations = relationRecord(snapshot);
    for (const key of Object.keys(relations)) {
        for (const link of relations[key] ?? []) {
            if (getAliveEntityById(world, link.targetId) === undefined) {
                fail(`Relation target entity ${link.targetId} does not exist.`);
            }
        }
    }
}

function applyTrait(world: World, entity: Entity, trait: Trait, value: object | true): void {
    if (!hasTrait(world, entity, trait)) addTrait(world, entity, trait);
    if (value === true) return;

    const next = deepCopy(value);
    if (!shallowEqual(getTrait(world, entity, trait), next)) {
        setTrait(world, entity, trait, next, true);
    }
}

function applyRelation(
    world: World,
    entity: Entity,
    relation: Relation<Trait>,
    links: RelationLinkSnapshot[]
): void {
    const desired = new Set<number>();
    for (const link of links) desired.add(link.targetId);

    for (const target of getRelationTargets(world, relation, entity)) {
        if (!desired.has(getEntityId(target))) removeTrait(world, entity, relation(target));
    }

    for (const link of links) {
        const target = getAliveEntityById(world, link.targetId);
        if (target === undefined) fail(`Relation target entity ${link.targetId} does not exist.`);

        if (!hasRelationToTarget(world, relation, entity, target)) {
            addTrait(world, entity, relation(target));
        }

        if (link.data === undefined) continue;
        const next = deepCopy(link.data);
        const current = getRelationData(world, entity, relation, target);
        if (!shallowEqual(current, next)) {
            setTrait(world, entity, relation(target), next as Record<string, unknown>, true);
        }
    }
}

/**
 * Makes `entity` match `snapshot`.
 * Traits and relations missing from the snapshot are removed first, then the
 * snapshot's traits and relations are added or updated.
 */
export function rollbackEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
): void {
    if (snapshot == null) fail('Cannot rollback a null or undefined entity snapshot.');
    assertEntityAlive(world, entity, 'rollback');
    const internal = registryInternal(registry);
    assertSnapshotKeys(internal, snapshot);
    assertRelationTargetsExist(world, snapshot);

    const worldEntity = world[$internal].worldEntity;
    const traitKeys = traitRecord(snapshot);
    const relationKeys = relationRecord(snapshot);
    const current = [...(world[$internal].entityTraits.get(entity) ?? [])];

    for (const trait of current) {
        if (entity === worldEntity && trait === IsExcluded) continue;

        const relation = trait[$internal].relation;
        if (relation) {
            const key = internal.keysByRelation.get(relation);
            const links = key == null ? undefined : relationKeys[key];
            if (key == null || links == null || links.length === 0) removeTrait(world, entity, trait);
            continue;
        }

        const key = internal.keysByTrait.get(trait);
        if (key == null || !Object.prototype.hasOwnProperty.call(traitKeys, key)) {
            removeTrait(world, entity, trait);
        }
    }

    for (const key of Object.keys(traitKeys)) {
        applyTrait(world, entity, assertKnownTraitKey(internal, key), traitKeys[key]);
    }

    for (const key of Object.keys(relationKeys)) {
        const links = relationKeys[key] ?? [];
        if (links.length === 0) continue;
        applyRelation(world, entity, assertKnownRelationKey(internal, key), links);
    }
}

function assertCheckpoint(world: World, registry: RegistryInternal, checkpoint: WorldSnapshot): void {
    if (checkpoint == null || !Array.isArray(checkpoint.entities)) {
        fail('World snapshot must have an entities array.');
    }

    const aliveIds = new Set<number>();
    const worldEntity = world[$internal].worldEntity;
    if (worldEntity != null) aliveIds.add(getEntityId(worldEntity));

    for (const snapshot of checkpoint.entities) {
        if (snapshot == null || typeof snapshot.id !== 'number' || !Number.isInteger(snapshot.id)) {
            fail('World checkpoint has an invalid entity snapshot.');
        }
        if (snapshot.id < 0) fail(`Invalid entity id ${snapshot.id}.`);
        aliveIds.add(snapshot.id);
        assertSnapshotKeys(registry, snapshot);
    }

    for (const snapshot of checkpoint.entities) {
        const relations = relationRecord(snapshot);
        for (const key of Object.keys(relations)) {
            for (const link of relations[key] ?? []) {
                if (!aliveIds.has(link.targetId)) fail(`Dangling relation target ${link.targetId}.`);
            }
        }
    }
}

function recreateEntities(world: World, ids: number[]): Map<number, Entity> {
    const worldEntity = world[$internal].worldEntity;
    const wanted = new Set(ids);
    let maxId = worldEntity == null ? -1 : getEntityId(worldEntity);
    for (const id of ids) if (id > maxId) maxId = id;

    const index = world[$internal].entityIndex;
    while (index.maxId <= maxId) world.spawn();

    for (const entity of world.entities) {
        if (entity === worldEntity) continue;
        if (wanted.has(getEntityId(entity))) continue;
        if (world.has(entity)) entity.destroy();
    }

    const byId = new Map<number, Entity>();
    for (const entity of world.entities) byId.set(getEntityId(entity), entity);
    return byId;
}

/**
 * Replaces the world's entities with the checkpoint.
 * Recreated entities use the same ids as the checkpoint. The internal world
 * entity is left as the world's own singleton entity.
 */
export function rollbackWorld(
    world: World,
    registry: TraitRegistry,
    checkpoint: WorldSnapshot
): void {
    const internal = registryInternal(registry);
    assertCheckpoint(world, internal, checkpoint);

    const ids = checkpoint.entities.map((snapshot) => snapshot.id);
    world.reset();
    const byId = recreateEntities(world, ids);

    for (const snapshot of checkpoint.entities) {
        const entity = byId.get(snapshot.id);
        if (entity === undefined) fail(`Failed to recreate entity ${snapshot.id}.`);
        rollbackEntity(world, entity, registry, snapshot);
    }
}

/** Trait-only diff. Relation changes are reported by `diffWorldSnapshots`. */
export function diffEntitySnapshots(a: EntitySnapshot, b: EntitySnapshot): EntitySnapshotDiff {
    if (a == null || b == null) fail('Cannot diff null or undefined entity snapshots.');

    const aTraits = traitRecord(a);
    const bTraits = traitRecord(b);
    const addedTraits: string[] = [];
    const removedTraits: string[] = [];
    const changedTraits: string[] = [];
    const keys = new Set([...Object.keys(aTraits), ...Object.keys(bTraits)]);

    for (const key of keys) {
        const inA = Object.prototype.hasOwnProperty.call(aTraits, key);
        const inB = Object.prototype.hasOwnProperty.call(bTraits, key);
        if (inA && !inB) removedTraits.push(key);
        else if (!inA && inB) addedTraits.push(key);
        else if (!shallowEqual(aTraits[key], bTraits[key])) changedTraits.push(key);
    }

    return {
        addedTraits: sortStrings(addedTraits),
        removedTraits: sortStrings(removedTraits),
        changedTraits: sortStrings(changedTraits),
    };
}

/**
 * Entity-id diff of two world snapshots.
 * Trait key order, relation key order, and relation target order are ignored.
 * An empty `relations` object matches a snapshot that omits `relations`.
 */
export function diffWorldSnapshots(before: WorldSnapshot, after: WorldSnapshot): WorldSnapshotDiff {
    if (before == null || after == null) fail('Cannot diff null or undefined world snapshots.');
    if (!Array.isArray(before.entities) || !Array.isArray(after.entities)) {
        fail('World snapshot must have an entities array.');
    }

    const beforeMap = new Map<number, EntitySnapshot>();
    const afterMap = new Map<number, EntitySnapshot>();
    for (const snapshot of before.entities) beforeMap.set(snapshot.id, snapshot);
    for (const snapshot of after.entities) afterMap.set(snapshot.id, snapshot);

    const added: number[] = [];
    const removed: number[] = [];
    const changed: number[] = [];

    for (const [id, snapshot] of beforeMap) {
        const other = afterMap.get(id);
        if (!other) removed.push(id);
        else if (!entityContentEqual(snapshot, other)) changed.push(id);
    }

    for (const id of afterMap.keys()) {
        if (!beforeMap.has(id)) added.push(id);
    }

    return {
        added: sortNumbers(added),
        removed: sortNumbers(removed),
        changed: sortNumbers(changed),
    };
}

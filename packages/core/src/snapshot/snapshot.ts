import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { isEntityAlive } from '../entity/utils/entity-index';
import { getEntityGeneration, getEntityId } from '../entity/utils/pack-entity';
import { IsExcluded } from '../query/query';
import { getRelationData, getRelationTargets } from '../relation/relation';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import type { Trait } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world';
import { deepCopy } from './deep-copy';
import type {
    EntitySnapshot,
    EntitySnapshotDiff,
    RelationSnapshot,
    TraitRegistry,
    WorldSnapshot,
    WorldSnapshotDiff,
} from './types';

function relationHasStore(relation: Relation<Trait>): boolean {
    return relation[$internal].trait[$internal].type !== 'tag';
}

function findEntityById(world: World, id: number): Entity | undefined {
    const index = world[$internal].entityIndex;
    const denseIndex = index.sparse[id];
    if (denseIndex === undefined || denseIndex >= index.aliveCount) return undefined;

    const entity = index.dense[denseIndex];
    if (getEntityId(entity) !== id) return undefined;
    if (!isEntityAlive(index, entity)) return undefined;
    return entity;
}

function snapshotRelation(
    world: World,
    entity: Entity,
    relation: Relation<Trait>
): RelationSnapshot[] {
    const hasStore = relationHasStore(relation);
    const links: RelationSnapshot[] = [];

    for (const target of getRelationTargets(world, relation, entity)) {
        const link: RelationSnapshot = { targetId: getEntityId(target) };
        if (hasStore) {
            const data = getRelationData(world, entity, relation, target);
            link.data = (data === undefined ? {} : deepCopy(data)) as object;
        }
        links.push(link);
    }

    return links;
}

function assertKnownKeys(registry: TraitRegistry, snapshot: EntitySnapshot): void {
    const traits = snapshot.traits ?? {};
    for (const key of Object.keys(traits)) {
        const value = registry.get(key);
        if (!value || isRelation(value)) {
            throw new Error(`Koota: Unknown registry key "${key}".`);
        }
    }

    const relations = snapshot.relations ?? {};
    for (const key of Object.keys(relations)) {
        const value = registry.get(key);
        if (!value || !isRelation(value)) {
            throw new Error(`Koota: Unknown registry key "${key}".`);
        }
    }
}

function assertTargetsExist(world: World, snapshot: EntitySnapshot): void {
    const relations = snapshot.relations ?? {};
    for (const key of Object.keys(relations)) {
        for (const link of relations[key]!) {
            if (findEntityById(world, link.targetId) === undefined) {
                throw new Error(`Koota: Relation target entity ${link.targetId} does not exist.`);
            }
        }
    }
}

function assertNoDanglingTargets(world: World, checkpoint: WorldSnapshot): void {
    const liveIds = new Set<number>();
    for (const snapshot of checkpoint.entities) liveIds.add(snapshot.id);
    liveIds.add(getEntityId(world[$internal].worldEntity));

    for (const snapshot of checkpoint.entities) {
        const relations = snapshot.relations ?? {};
        for (const key of Object.keys(relations)) {
            for (const link of relations[key]!) {
                if (!liveIds.has(link.targetId)) {
                    throw new Error(`Koota: Dangling relation target ${link.targetId}.`);
                }
            }
        }
    }
}

function applyTrait(world: World, entity: Entity, trait: Trait, value: object | true): void {
    if (trait[$internal].type === 'tag') {
        if (!hasTrait(world, entity, trait)) addTrait(world, entity, trait);
        return;
    }

    const data = deepCopy(value);
    if (!hasTrait(world, entity, trait)) {
        addTrait(world, entity, [trait, data]);
        return;
    }
    setTrait(world, entity, trait, data, true);
}

function applyRelation(
    world: World,
    entity: Entity,
    relation: Relation<Trait>,
    links: RelationSnapshot[]
): void {
    for (const target of getRelationTargets(world, relation, entity)) {
        removeTrait(world, entity, relation(target));
    }

    const hasStore = relationHasStore(relation);
    for (const link of links) {
        const target = findEntityById(world, link.targetId);
        if (target === undefined) {
            throw new Error(`Koota: Relation target entity ${link.targetId} does not exist.`);
        }
        if (hasStore && link.data !== undefined) {
            addTrait(world, entity, relation(target, deepCopy(link.data) as Record<string, unknown>));
        } else {
            addTrait(world, entity, relation(target));
        }
    }
}

/**
 * Capture one entity. Tag traits are stored as `true`, data traits and relation
 * stores are deep-copied. `relations` is omitted when the entity has none.
 */
export function snapshotEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry
): EntitySnapshot {
    if (!world.has(entity)) throw new Error('Koota: Cannot snapshot a destroyed entity.');

    const traitSet = world[$internal].entityTraits.get(entity);
    const traits: Record<string, object | true> = {};
    const relations: Record<string, RelationSnapshot[]> = {};
    let hasRelations = false;

    if (traitSet) {
        for (const trait of traitSet) {
            // IsExcluded marks the internal world entity. Ignore it unless the caller
            // explicitly registered that tag.
            if (trait === IsExcluded && registry.keyForTrait(trait) === undefined) continue;

            const owningRelation = trait[$internal].relation;
            if (owningRelation) {
                const key = registry.keyForRelation(owningRelation);
                if (key === undefined) throw new Error('Koota: Unregistered relation on entity.');

                const links = snapshotRelation(world, entity, owningRelation);
                if (links.length > 0) {
                    relations[key] = links;
                    hasRelations = true;
                }
                continue;
            }

            const key = registry.keyForTrait(trait);
            if (key === undefined) throw new Error('Koota: Unregistered trait on entity.');

            if (trait[$internal].type === 'tag') {
                traits[key] = true;
            } else {
                traits[key] = deepCopy(getTrait(world, entity, trait)) as object;
            }
        }
    }

    const snapshot: EntitySnapshot = { id: getEntityId(entity), traits };
    if (hasRelations) snapshot.relations = relations;
    return snapshot;
}

/** Capture every alive entity except the internal world entity. */
export function snapshotWorld(world: World, registry: TraitRegistry): WorldSnapshot {
    const worldEntity = world[$internal].worldEntity;
    const entities: EntitySnapshot[] = [];

    for (const entity of world.entities) {
        if (entity === worldEntity) continue;
        entities.push(snapshotEntity(world, entity, registry));
    }

    entities.sort((a, b) => a.id - b.id);
    return { entities };
}

/**
 * Make `entity` match `snapshot`. Traits and relations absent from the snapshot
 * are removed first; the rest are added or overwritten.
 */
export function rollbackEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
): void {
    if (snapshot == null) throw new Error('Koota: Cannot rollback a null or undefined snapshot.');
    if (!world.has(entity)) throw new Error('Koota: Cannot rollback a destroyed entity.');

    assertKnownKeys(registry, snapshot);
    assertTargetsExist(world, snapshot);

    const current = world[$internal].entityTraits.get(entity);
    const traitList = current ? [...current] : [];
    const relationKeys = new Set(Object.keys(snapshot.relations ?? {}));

    for (const trait of traitList) {
        if (trait === IsExcluded && registry.keyForTrait(trait) === undefined) continue;

        const owningRelation = trait[$internal].relation;
        if (owningRelation) {
            const key = registry.keyForRelation(owningRelation);
            if (key === undefined || !relationKeys.has(key)) removeTrait(world, entity, trait);
            continue;
        }

        const key = registry.keyForTrait(trait);
        if (key === undefined || !Object.prototype.hasOwnProperty.call(snapshot.traits ?? {}, key)) {
            removeTrait(world, entity, trait);
        }
    }

    const traits = snapshot.traits ?? {};
    for (const key of Object.keys(traits)) {
        applyTrait(world, entity, registry.get(key) as Trait, traits[key]!);
    }

    const relations = snapshot.relations ?? {};
    for (const key of Object.keys(relations)) {
        applyRelation(world, entity, registry.get(key) as Relation<Trait>, relations[key]!);
    }
}

/**
 * Replace world contents with `checkpoint`, recreating entities at the same ids
 * (`entity.id()`). Generation is reset to 0, so never-recycled entity references
 * stay valid.
 */
export function rollbackWorld(
    world: World,
    registry: TraitRegistry,
    checkpoint: WorldSnapshot
): void {
    if (checkpoint == null || !Array.isArray(checkpoint.entities)) {
        throw new Error('Koota: World snapshot is missing an entities array.');
    }

    for (const snapshot of checkpoint.entities) assertKnownKeys(registry, snapshot);
    assertNoDanglingTargets(world, checkpoint);

    const snapshots = checkpoint.entities;
    world.reset();

    const byId = materializeEntities(
        world,
        snapshots.map((snapshot) => snapshot.id)
    );

    for (const snapshot of snapshots) {
        const entity = byId.get(snapshot.id);
        if (entity === undefined) {
            throw new Error(`Koota: Failed to recreate entity ${snapshot.id}.`);
        }
        rollbackEntity(world, entity, registry, snapshot);
    }
}

/**
 * Spawn a contiguous id range, then drop ids that are not in the checkpoint.
 * Survivors keep generation 0, so their packed entity value matches a fresh spawn.
 */
function materializeEntities(world: World, ids: number[]): Map<number, Entity> {
    let maxId = 0;
    const wanted = new Set<number>();
    for (const id of ids) {
        wanted.add(id);
        if (id > maxId) maxId = id;
    }

    const spawned: Entity[] = [];
    for (let id = 1; id <= maxId; id++) {
        const entity = world.spawn();
        if (getEntityId(entity) !== id || getEntityGeneration(entity) !== 0) {
            throw new Error(`Koota: Failed to recreate entity ${id}.`);
        }
        spawned.push(entity);
    }

    const byId = new Map<number, Entity>();
    const worldEntity = world[$internal].worldEntity;
    const worldEntityId = getEntityId(worldEntity);
    if (wanted.has(worldEntityId)) byId.set(worldEntityId, worldEntity);

    for (const entity of spawned) {
        const id = getEntityId(entity);
        if (wanted.has(id)) byId.set(id, entity);
        else entity.destroy();
    }

    return byId;
}

function traitsShallowEqual(
    a: Record<string, object | true> | undefined,
    b: Record<string, object | true> | undefined
): boolean {
    const left = a ?? {};
    const right = b ?? {};
    const leftKeys = Object.keys(left);
    if (leftKeys.length !== Object.keys(right).length) return false;

    for (const key of leftKeys) {
        if (!Object.prototype.hasOwnProperty.call(right, key)) return false;
        if (!shallowEqual(left[key], right[key])) return false;
    }
    return true;
}

function relationDataEqual(a: object | undefined, b: object | undefined): boolean {
    if (a === undefined || b === undefined) return a === b;
    return shallowEqual(a, b);
}

function relationLinksEqual(a: RelationSnapshot[], b: RelationSnapshot[]): boolean {
    if (a.length !== b.length) return false;

    const left = [...a].sort((x, y) => x.targetId - y.targetId);
    const right = [...b].sort((x, y) => x.targetId - y.targetId);

    for (let i = 0; i < left.length; i++) {
        if (left[i]!.targetId !== right[i]!.targetId) return false;
        if (!relationDataEqual(left[i]!.data, right[i]!.data)) return false;
    }
    return true;
}

function relationsEqual(a: EntitySnapshot['relations'], b: EntitySnapshot['relations']): boolean {
    const left = a ?? {};
    const right = b ?? {};
    const leftKeys = Object.keys(left);
    if (leftKeys.length !== Object.keys(right).length) return false;

    for (const key of leftKeys) {
        if (!Object.prototype.hasOwnProperty.call(right, key)) return false;
        if (!relationLinksEqual(left[key]!, right[key]!)) return false;
    }
    return true;
}

/** Trait diff from `a` to `b`. Arrays are sorted ascending. Relation links are ignored. */
export function diffEntitySnapshots(
    a: EntitySnapshot | null | undefined,
    b: EntitySnapshot | null | undefined
): EntitySnapshotDiff {
    if (a == null || b == null) {
        throw new Error('Koota: Cannot diff null or undefined snapshots.');
    }

    const left = a.traits ?? {};
    const right = b.traits ?? {};
    const addedTraits: string[] = [];
    const removedTraits: string[] = [];
    const changedTraits: string[] = [];

    for (const key of Object.keys(left)) {
        if (!Object.prototype.hasOwnProperty.call(right, key)) {
            removedTraits.push(key);
        } else if (!shallowEqual(left[key], right[key])) {
            changedTraits.push(key);
        }
    }

    for (const key of Object.keys(right)) {
        if (!Object.prototype.hasOwnProperty.call(left, key)) addedTraits.push(key);
    }

    addedTraits.sort();
    removedTraits.sort();
    changedTraits.sort();

    return { addedTraits, removedTraits, changedTraits };
}

/**
 * Entity diff from `before` to `after`. Trait and relation key order, and relation
 * target order, do not affect equality. An empty `relations` object matches a
 * missing `relations` key.
 */
export function diffWorldSnapshots(
    before: WorldSnapshot | null | undefined,
    after: WorldSnapshot | null | undefined
): WorldSnapshotDiff {
    if (before == null || after == null) {
        throw new Error('Koota: Cannot diff null or undefined snapshots.');
    }
    if (!Array.isArray(before.entities) || !Array.isArray(after.entities)) {
        throw new Error('Koota: World snapshot is missing an entities array.');
    }

    const beforeById = new Map<number, EntitySnapshot>();
    const afterById = new Map<number, EntitySnapshot>();
    for (const snapshot of before.entities) beforeById.set(snapshot.id, snapshot);
    for (const snapshot of after.entities) afterById.set(snapshot.id, snapshot);

    const added: number[] = [];
    const removed: number[] = [];
    const changed: number[] = [];

    for (const [id, snapshot] of beforeById) {
        const next = afterById.get(id);
        if (!next) {
            removed.push(id);
            continue;
        }
        if (
            !traitsShallowEqual(snapshot.traits, next.traits) ||
            !relationsEqual(snapshot.relations, next.relations)
        ) {
            changed.push(id);
        }
    }

    for (const id of afterById.keys()) {
        if (!beforeById.has(id)) added.push(id);
    }

    added.sort((x, y) => x - y);
    removed.sort((x, y) => x - y);
    changed.sort((x, y) => x - y);

    return { added, removed, changed };
}

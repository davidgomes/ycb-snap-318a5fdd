import { $internal } from '../common';
import { createEntityWithId, destroyEntity } from '../entity/entity';
import type { Entity } from '../entity/types';
import { ENTITY_ID_MASK, getEntityId } from '../entity/utils/pack-entity';
import { IsExcluded } from '../query/query';
import { getRelationData, getRelationTargets, hasRelationToTarget } from '../relation/relation';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import type { Trait } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world';
import type {
    EntitySnapshot,
    EntitySnapshotDiff,
    RelationTargetSnapshot,
    TraitRegistry,
    TraitRegistryEntry,
    WorldSnapshot,
    WorldSnapshotDiff,
} from './types';

type ResolvedSnapshot = {
    traits: Map<Trait, object | true>;
    relations: Map<Relation<Trait>, RelationTargetSnapshot[]>;
};

export function createTraitRegistry(...entries: TraitRegistryEntry[]): TraitRegistry {
    const byKey = new Map<string, Trait | Relation<Trait>>();
    const traitKeys = new Map<Trait, string>();
    const relationKeys = new Map<Relation<Trait>, string>();

    for (const entry of entries) {
        if (!Array.isArray(entry) || typeof entry[0] !== 'string' || typeof entry[1] !== 'function') {
            throw new Error('Koota: Trait registry entries must be [key, trait | relation] tuples.');
        }

        const [key, value] = entry;

        if (byKey.has(key)) {
            throw new Error(`Koota: Duplicate trait registry key "${key}".`);
        }

        if (isRelation(value)) {
            if (relationKeys.has(value)) {
                throw new Error(
                    `Koota: Relation registered as "${key}" is already registered as "${relationKeys.get(value)}".`
                );
            }
            relationKeys.set(value, key);
        } else {
            if (traitKeys.has(value)) {
                throw new Error(
                    `Koota: Trait registered as "${key}" is already registered as "${traitKeys.get(value)}".`
                );
            }
            traitKeys.set(value, key);
        }

        byKey.set(key, value);
    }

    return {
        keys: Array.from(byKey.keys()),
        [$internal]: { entries: byKey, traitKeys, relationKeys },
    };
}

export function snapshotEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry
): EntitySnapshot {
    if (!world.has(entity)) {
        throw new Error('Koota: Cannot snapshot an entity that does not exist.');
    }

    const { traitKeys, relationKeys } = registry[$internal];
    const traits: Record<string, object | true> = {};
    let relations: Record<string, RelationTargetSnapshot[]> | undefined;

    for (const trait of world[$internal].entityTraits.get(entity)!) {
        if (trait === IsExcluded) continue;

        const traitCtx = trait[$internal];
        const relation = traitCtx.relation as Relation<Trait> | null;

        if (relation) {
            const key = relationKeys.get(relation);
            if (key === undefined) {
                throw new Error('Koota: Cannot snapshot an entity with an unregistered relation.');
            }

            const hasStore = traitCtx.type !== 'tag';
            relations ??= {};
            relations[key] = getRelationTargets(world, relation, entity).map((target) => {
                const targetSnapshot: RelationTargetSnapshot = {
                    targetId: getEntityId(target),
                };
                if (hasStore) {
                    const data = getRelationData(world, entity, relation, target);
                    if (data !== undefined) targetSnapshot.data = deepClone(data) as object;
                }
                return targetSnapshot;
            });
            continue;
        }

        const key = traitKeys.get(trait);
        if (key === undefined) {
            throw new Error('Koota: Cannot snapshot an entity with an unregistered trait.');
        }

        traits[key] =
            traitCtx.type === 'tag' ? true : (deepClone(getTrait(world, entity, trait)) as object);
    }

    const snapshot: EntitySnapshot = { id: getEntityId(entity), traits };
    if (relations) snapshot.relations = relations;
    return snapshot;
}

export function snapshotWorld(world: World, registry: TraitRegistry): WorldSnapshot {
    const worldEntity = world[$internal].worldEntity;
    const entities = world.entities
        .filter((entity) => entity !== worldEntity)
        .sort((a, b) => getEntityId(a) - getEntityId(b))
        .map((entity) => snapshotEntity(world, entity, registry));

    return { entities };
}

export function rollbackEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
): void {
    if (!world.has(entity)) {
        throw new Error('Koota: Cannot roll back an entity that does not exist.');
    }

    const resolved = resolveSnapshot(registry, snapshot);

    for (const targets of resolved.relations.values()) {
        for (const { targetId } of targets) {
            if (findEntityById(world, targetId) === undefined) {
                throw new Error(`Koota: Relation target entity ${targetId} does not exist.`);
            }
        }
    }

    applySnapshot(world, entity, resolved);
}

export function rollbackWorld(
    world: World,
    registry: TraitRegistry,
    checkpoint: WorldSnapshot
): void {
    if (!checkpoint || !Array.isArray(checkpoint.entities)) {
        throw new Error('Koota: A world checkpoint must have an entities array.');
    }

    if (!world.isInitialized) world.init();

    const ctx = world[$internal];
    const worldEntityId = getEntityId(ctx.worldEntity);
    const ids = new Set<number>();

    for (const snapshot of checkpoint.entities) {
        const id = snapshot?.id;
        if (!Number.isInteger(id) || id < 0 || id > ENTITY_ID_MASK) {
            throw new Error(`Koota: Invalid entity ID ${id} in checkpoint.`);
        }
        if (ids.has(id)) throw new Error(`Koota: Duplicate entity ID ${id} in checkpoint.`);
        if (id === worldEntityId) {
            throw new Error(`Koota: Entity ID ${id} is reserved by the world entity.`);
        }
        ids.add(id);
    }

    const resolvedSnapshots = checkpoint.entities.map((snapshot) => {
        const resolved = resolveSnapshot(registry, snapshot);
        for (const targets of resolved.relations.values()) {
            for (const { targetId } of targets) {
                if (!ids.has(targetId)) {
                    throw new Error(
                        `Koota: Relation target entity ${targetId} does not exist in the checkpoint.`
                    );
                }
            }
        }
        return resolved;
    });

    for (const entity of world.entities) {
        if (entity !== ctx.worldEntity && world.has(entity)) destroyEntity(world, entity);
    }

    const created = checkpoint.entities
        .map((snapshot, index) => ({ id: snapshot.id, index }))
        .sort((a, b) => a.id - b.id)
        .map(({ id, index }) => ({ entity: createEntityWithId(world, id), index }));

    for (const { entity, index } of created) {
        applySnapshot(world, entity, resolvedSnapshots[index]);
    }
}

export function diffEntitySnapshots(a: EntitySnapshot, b: EntitySnapshot): EntitySnapshotDiff {
    if (a == null || b == null) {
        throw new Error('Koota: Cannot diff a missing entity snapshot.');
    }

    const addedTraits: string[] = [];
    const removedTraits: string[] = [];
    const changedTraits: string[] = [];

    for (const key of Object.keys(b.traits)) {
        if (!Object.hasOwn(a.traits, key)) addedTraits.push(key);
        else if (!shallowEqual(a.traits[key], b.traits[key])) changedTraits.push(key);
    }

    for (const key of Object.keys(a.traits)) {
        if (!Object.hasOwn(b.traits, key)) removedTraits.push(key);
    }

    return {
        addedTraits: addedTraits.sort(),
        removedTraits: removedTraits.sort(),
        changedTraits: changedTraits.sort(),
    };
}

export function diffWorldSnapshots(before: WorldSnapshot, after: WorldSnapshot): WorldSnapshotDiff {
    if (!before || !Array.isArray(before.entities) || !after || !Array.isArray(after.entities)) {
        throw new Error('Koota: Cannot diff world snapshots without entities arrays.');
    }

    const beforeById = new Map(before.entities.map((snapshot) => [snapshot.id, snapshot]));
    const afterById = new Map(after.entities.map((snapshot) => [snapshot.id, snapshot]));

    const added: number[] = [];
    const removed: number[] = [];
    const changed: number[] = [];

    for (const [id, snapshot] of afterById) {
        const previous = beforeById.get(id);
        if (!previous) added.push(id);
        else if (!entitySnapshotsEqual(previous, snapshot)) changed.push(id);
    }

    for (const id of beforeById.keys()) {
        if (!afterById.has(id)) removed.push(id);
    }

    const ascending = (x: number, y: number) => x - y;
    return {
        added: added.sort(ascending),
        removed: removed.sort(ascending),
        changed: changed.sort(ascending),
    };
}

function resolveSnapshot(registry: TraitRegistry, snapshot: EntitySnapshot): ResolvedSnapshot {
    if (!snapshot || typeof snapshot.traits !== 'object' || snapshot.traits === null) {
        throw new Error('Koota: An entity snapshot must have a traits record.');
    }

    const entries = registry[$internal].entries;
    const traits = new Map<Trait, object | true>();
    const relations = new Map<Relation<Trait>, RelationTargetSnapshot[]>();

    for (const key of Object.keys(snapshot.traits)) {
        const trait = entries.get(key);
        if (trait === undefined || isRelation(trait)) {
            throw new Error(`Koota: Unknown trait registry key "${key}".`);
        }
        traits.set(trait, snapshot.traits[key]);
    }

    if (snapshot.relations) {
        for (const key of Object.keys(snapshot.relations)) {
            const relation = entries.get(key);
            if (relation === undefined || !isRelation(relation)) {
                throw new Error(`Koota: Unknown relation registry key "${key}".`);
            }
            relations.set(relation, snapshot.relations[key]);
        }
    }

    return { traits, relations };
}

function applySnapshot(world: World, entity: Entity, resolved: ResolvedSnapshot): void {
    const currentTraits = Array.from(world[$internal].entityTraits.get(entity)!);

    for (const trait of currentTraits) {
        if (trait === IsExcluded) continue;

        const relation = trait[$internal].relation as Relation<Trait> | null;

        if (!relation) {
            if (!resolved.traits.has(trait)) removeTrait(world, entity, trait);
            continue;
        }

        const desired = resolved.relations.get(relation);
        if (!desired || desired.length === 0) {
            removeTrait(world, entity, trait);
            continue;
        }

        const desiredIds = new Set(desired.map(({ targetId }) => targetId));
        for (const target of getRelationTargets(world, relation, entity)) {
            if (!desiredIds.has(getEntityId(target))) removeTrait(world, entity, relation(target));
        }
    }

    for (const [trait, value] of resolved.traits) {
        if (value === true) {
            addTrait(world, entity, trait);
        } else if (hasTrait(world, entity, trait)) {
            setTrait(world, entity, trait, deepClone(value));
        } else {
            addTrait(world, entity, [trait, deepClone(value) as Record<string, unknown>]);
        }
    }

    for (const [relation, targets] of resolved.relations) {
        for (const { targetId, data } of targets) {
            const target = findEntityById(world, targetId)!;
            const params =
                data === undefined ? undefined : (deepClone(data) as Record<string, unknown>);

            if (!hasRelationToTarget(world, relation, entity, target)) {
                addTrait(world, entity, relation(target, params));
            } else if (params !== undefined) {
                setTrait(world, entity, relation(target), params);
            }
        }
    }
}

function findEntityById(world: World, id: number): Entity | undefined {
    const { sparse, dense, aliveCount } = world[$internal].entityIndex;
    const denseIndex = sparse[id];
    if (denseIndex === undefined || denseIndex >= aliveCount) return undefined;
    return dense[denseIndex];
}

function entitySnapshotsEqual(a: EntitySnapshot, b: EntitySnapshot): boolean {
    return (
        recordsEqual(a.traits, b.traits, shallowEqual) &&
        recordsEqual(nonEmptyRelations(a), nonEmptyRelations(b), relationTargetsEqual)
    );
}

function nonEmptyRelations(snapshot: EntitySnapshot): Record<string, RelationTargetSnapshot[]> {
    const result: Record<string, RelationTargetSnapshot[]> = {};
    if (!snapshot.relations) return result;
    for (const key of Object.keys(snapshot.relations)) {
        if (snapshot.relations[key].length > 0) result[key] = snapshot.relations[key];
    }
    return result;
}

function recordsEqual<T>(
    a: Record<string, T>,
    b: Record<string, T>,
    equal: (x: T, y: T) => boolean
): boolean {
    const keys = Object.keys(a);
    if (keys.length !== Object.keys(b).length) return false;
    return keys.every((key) => Object.hasOwn(b, key) && equal(a[key], b[key]));
}

function relationTargetsEqual(a: RelationTargetSnapshot[], b: RelationTargetSnapshot[]): boolean {
    if (a.length !== b.length) return false;
    const bByTarget = new Map(b.map((target) => [target.targetId, target]));
    if (bByTarget.size !== b.length) return false;
    return a.every((target) => {
        const other = bByTarget.get(target.targetId);
        return other !== undefined && shallowEqual(target.data, other.data);
    });
}

function deepClone<T>(value: T): T {
    if (typeof value !== 'object' || value === null) return value;

    if (Array.isArray(value)) return value.map(deepClone) as T;
    if (value instanceof Date) return new Date(value.getTime()) as T;
    if (value instanceof Map) {
        return new Map(Array.from(value, ([k, v]) => [deepClone(k), deepClone(v)])) as T;
    }
    if (value instanceof Set) return new Set(Array.from(value, deepClone)) as T;
    if (ArrayBuffer.isView(value) && !(value instanceof DataView))
        return (value as unknown as { slice(): T }).slice();

    const clone = Object.create(Object.getPrototypeOf(value));
    for (const key of Object.keys(value)) {
        clone[key] = deepClone((value as Record<string, unknown>)[key]);
    }
    return clone;
}

import { $internal } from '../common';
import { createEntityWithId, destroyEntity } from '../entity/entity';
import type { Entity } from '../entity/types';
import { getAliveEntities, isEntityAlive } from '../entity/utils/entity-index';
import { getEntityId } from '../entity/utils/pack-entity';
import { getRelationData, getRelationTargets, hasRelationPair } from '../relation/relation';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import type { Trait } from '../trait/types';
import type { World } from '../world';

export type TraitRegistryEntry = [string, Trait | Relation<any>];

export type TraitRegistry = {
    readonly traits: ReadonlyMap<string, Trait>;
    readonly relations: ReadonlyMap<string, Relation<Trait>>;
    readonly traitKeys: ReadonlyMap<Trait, string>;
    readonly relationKeys: ReadonlyMap<Relation<Trait>, string>;
};

export type RelationSnapshot = { targetId: number; data?: object };

export type EntitySnapshot = {
    id: number;
    traits: Record<string, object | true>;
    relations?: Record<string, RelationSnapshot[]>;
};

export type WorldSnapshot = { entities: EntitySnapshot[] };

export type EntitySnapshotDiff = {
    addedTraits: string[];
    removedTraits: string[];
    changedTraits: string[];
};

export type WorldSnapshotDiff = { added: number[]; removed: number[]; changed: number[] };

export function createTraitRegistry(...entries: TraitRegistryEntry[]): TraitRegistry {
    const traits = new Map<string, Trait>();
    const relations = new Map<string, Relation<Trait>>();
    const traitKeys = new Map<Trait, string>();
    const relationKeys = new Map<Relation<Trait>, string>();

    for (const [key, value] of entries) {
        if (traits.has(key) || relations.has(key)) {
            throw new Error(`Koota: Duplicate registry key "${key}".`);
        }
        if (isRelation(value)) {
            if (relationKeys.has(value)) throw new Error(`Koota: Duplicate relation for key "${key}".`);
            relations.set(key, value);
            relationKeys.set(value, key);
        } else {
            const t = value as Trait;
            if (traitKeys.has(t)) throw new Error(`Koota: Duplicate trait for key "${key}".`);
            traits.set(key, t);
            traitKeys.set(t, key);
        }
    }

    return { traits, relations, traitKeys, relationKeys };
}

const clone = <T>(value: T): T => structuredClone(value);

const isTag = (trait: Trait) => trait[$internal].type === 'tag';

function assertAlive(world: World, entity: Entity) {
    if (!isEntityAlive(world[$internal].entityIndex, entity)) {
        throw new Error('Koota: The entity does not exist in the world.');
    }
}

function findEntityById(world: World, id: number): Entity | undefined {
    const index = world[$internal].entityIndex;
    const denseIndex = index.sparse[id];
    if (denseIndex === undefined || denseIndex >= index.aliveCount) return undefined;
    return index.dense[denseIndex];
}

export function snapshotEntity(world: World, entity: Entity, registry: TraitRegistry): EntitySnapshot {
    assertAlive(world, entity);

    const snapshot: EntitySnapshot = { id: getEntityId(entity), traits: {} };
    const relations: Record<string, RelationSnapshot[]> = {};
    let hasRelations = false;

    const entityTraits = world[$internal].entityTraits.get(entity) ?? new Set<Trait>();

    for (const trait of entityTraits) {
        const relation = trait[$internal].relation as Relation<Trait> | null;

        if (relation) {
            const key = registry.relationKeys.get(relation);
            if (key === undefined) throw new Error('Koota: Relation is not registered in the registry.');

            const withData = !isTag(trait);
            relations[key] = getRelationTargets(world, relation, entity).map((target) => {
                const entry: RelationSnapshot = { targetId: getEntityId(target) };
                if (withData) entry.data = clone(getRelationData(world, entity, relation, target) as object);
                return entry;
            });
            hasRelations = true;
            continue;
        }

        const key = registry.traitKeys.get(trait);
        if (key === undefined) throw new Error('Koota: Trait is not registered in the registry.');

        snapshot.traits[key] = isTag(trait) ? true : clone(getTrait(world, entity, trait) as object);
    }

    if (hasRelations) snapshot.relations = relations;

    return snapshot;
}

export function snapshotWorld(world: World, registry: TraitRegistry): WorldSnapshot {
    const ctx = world[$internal];
    const entities = getAliveEntities(ctx.entityIndex)
        .filter((entity) => entity !== ctx.worldEntity)
        .sort((a, b) => getEntityId(a) - getEntityId(b));

    return { entities: entities.map((entity) => snapshotEntity(world, entity, registry)) };
}

function validateSnapshotKeys(registry: TraitRegistry, snapshot: EntitySnapshot) {
    for (const key in snapshot.traits) {
        if (!registry.traits.has(key)) throw new Error(`Koota: Unknown trait key "${key}".`);
    }
    for (const key in snapshot.relations ?? {}) {
        if (!registry.relations.has(key)) throw new Error(`Koota: Unknown relation key "${key}".`);
    }
}

function applySnapshotTraits(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
) {
    for (const key in snapshot.traits) {
        const trait = registry.traits.get(key)!;
        const value = snapshot.traits[key];

        if (isTag(trait)) {
            if (!hasTrait(world, entity, trait)) addTrait(world, entity, trait);
        } else if (hasTrait(world, entity, trait)) {
            setTrait(world, entity, trait, clone(value));
        } else {
            addTrait(world, entity, [trait, clone(value)] as any);
        }
    }
}

function applySnapshotRelations(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
) {
    for (const key in snapshot.relations ?? {}) {
        const relation = registry.relations.get(key)!;
        for (const { targetId, data } of snapshot.relations![key]) {
            const target = findEntityById(world, targetId)!;
            const pair = relation(target);

            if (hasRelationPair(world, entity, pair)) {
                if (data !== undefined) setTrait(world, entity, pair, clone(data));
            } else {
                addTrait(world, entity, data !== undefined ? relation(target, clone(data) as any) : pair);
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
    assertAlive(world, entity);
    validateSnapshotKeys(registry, snapshot);

    for (const key in snapshot.relations ?? {}) {
        for (const { targetId } of snapshot.relations![key]) {
            if (findEntityById(world, targetId) === undefined) {
                throw new Error(`Koota: Relation target entity ${targetId} does not exist.`);
            }
        }
    }

    const entityTraits = [...(world[$internal].entityTraits.get(entity) ?? [])];

    for (const trait of entityTraits) {
        const relation = trait[$internal].relation as Relation<Trait> | null;

        if (relation) {
            const key = registry.relationKeys.get(relation);
            const wanted = new Set(
                (key !== undefined ? snapshot.relations?.[key] ?? [] : []).map((r) => r.targetId)
            );
            for (const target of getRelationTargets(world, relation, entity)) {
                if (!wanted.has(getEntityId(target))) removeTrait(world, entity, relation(target));
            }
            continue;
        }

        const key = registry.traitKeys.get(trait);
        if (key === undefined || !(key in snapshot.traits)) removeTrait(world, entity, trait);
    }

    applySnapshotTraits(world, entity, registry, snapshot);
    applySnapshotRelations(world, entity, registry, snapshot);
}

export function rollbackWorld(world: World, registry: TraitRegistry, checkpoint: WorldSnapshot) {
    if (!checkpoint || !Array.isArray(checkpoint.entities)) {
        throw new Error('Koota: Invalid world snapshot.');
    }

    const ctx = world[$internal];
    const worldEntityId = getEntityId(ctx.worldEntity);
    const ids = new Set<number>();

    for (const snapshot of checkpoint.entities) {
        validateSnapshotKeys(registry, snapshot);
        if (snapshot.id === worldEntityId || ids.has(snapshot.id)) {
            throw new Error(`Koota: Invalid entity ID ${snapshot.id} in world snapshot.`);
        }
        ids.add(snapshot.id);
    }

    for (const snapshot of checkpoint.entities) {
        for (const key in snapshot.relations ?? {}) {
            for (const { targetId } of snapshot.relations![key]) {
                if (!ids.has(targetId)) {
                    throw new Error(`Koota: Relation target entity ${targetId} does not exist.`);
                }
            }
        }
    }

    for (const entity of getAliveEntities(ctx.entityIndex)) {
        if (entity !== ctx.worldEntity && world.has(entity)) destroyEntity(world, entity);
    }

    const entities = checkpoint.entities.map((snapshot) => createEntityWithId(world, snapshot.id));

    checkpoint.entities.forEach((snapshot, i) =>
        applySnapshotTraits(world, entities[i], registry, snapshot)
    );
    checkpoint.entities.forEach((snapshot, i) =>
        applySnapshotRelations(world, entities[i], registry, snapshot)
    );
}

function shallowEqual(a: unknown, b: unknown): boolean {
    if (a === b) return true;
    if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false;
    const aKeys = Object.keys(a);
    const bKeys = Object.keys(b);
    if (aKeys.length !== bKeys.length) return false;
    for (const key of aKeys) {
        if (!Object.prototype.hasOwnProperty.call(b, key)) return false;
        if ((a as any)[key] !== (b as any)[key]) return false;
    }
    return true;
}

const byNumber = (a: number, b: number) => a - b;

export function diffEntitySnapshots(a: EntitySnapshot, b: EntitySnapshot): EntitySnapshotDiff {
    if (a == null || b == null) throw new Error('Koota: Cannot diff missing entity snapshots.');

    const aTraits = a.traits ?? {};
    const bTraits = b.traits ?? {};
    const addedTraits: string[] = [];
    const removedTraits: string[] = [];
    const changedTraits: string[] = [];

    for (const key of Object.keys(bTraits)) {
        if (!(key in aTraits)) addedTraits.push(key);
        else if (!shallowEqual(aTraits[key], bTraits[key])) changedTraits.push(key);
    }
    for (const key of Object.keys(aTraits)) {
        if (!(key in bTraits)) removedTraits.push(key);
    }

    return {
        addedTraits: addedTraits.sort(),
        removedTraits: removedTraits.sort(),
        changedTraits: changedTraits.sort(),
    };
}

function relationsEqual(
    a: Record<string, RelationSnapshot[]> = {},
    b: Record<string, RelationSnapshot[]> = {}
): boolean {
    const aKeys = Object.keys(a).filter((key) => a[key].length > 0);
    const bKeys = Object.keys(b).filter((key) => b[key].length > 0);
    if (aKeys.length !== bKeys.length) return false;

    for (const key of aKeys) {
        const bTargets = b[key];
        if (!bTargets || bTargets.length !== a[key].length) return false;
        const bById = new Map(bTargets.map((r) => [r.targetId, r]));
        for (const r of a[key]) {
            const other = bById.get(r.targetId);
            if (!other || !shallowEqual(r.data, other.data)) return false;
        }
    }
    return true;
}

function entitySnapshotsEqual(a: EntitySnapshot, b: EntitySnapshot): boolean {
    const diff = diffEntitySnapshots(a, b);
    if (diff.addedTraits.length || diff.removedTraits.length || diff.changedTraits.length) return false;
    return relationsEqual(a.relations, b.relations);
}

export function diffWorldSnapshots(before: WorldSnapshot, after: WorldSnapshot): WorldSnapshotDiff {
    if (!before || !Array.isArray(before.entities) || !after || !Array.isArray(after.entities)) {
        throw new Error('Koota: Cannot diff invalid world snapshots.');
    }

    const beforeById = new Map(before.entities.map((e) => [e.id, e]));
    const afterById = new Map(after.entities.map((e) => [e.id, e]));
    const added: number[] = [];
    const removed: number[] = [];
    const changed: number[] = [];

    for (const [id, snapshot] of afterById) {
        const prev = beforeById.get(id);
        if (!prev) added.push(id);
        else if (!entitySnapshotsEqual(prev, snapshot)) changed.push(id);
    }
    for (const id of beforeById.keys()) {
        if (!afterById.has(id)) removed.push(id);
    }

    return { added: added.sort(byNumber), removed: removed.sort(byNumber), changed: changed.sort(byNumber) };
}

import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { allocateEntityWithId } from '../entity/utils/entity-index';
import { getEntityId } from '../entity/utils/pack-entity';
import {
    getRelationData,
    getRelationTargets,
} from '../relation/relation';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import type { Trait } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world';

export type RelationTargetSnapshot = {
    targetId: number;
    data?: object;
};

export type EntitySnapshot = {
    id: number;
    traits: Record<string, object | true>;
    relations?: Record<string, RelationTargetSnapshot[]>;
};

export type WorldSnapshot = {
    entities: EntitySnapshot[];
};

export type EntitySnapshotDiff = {
    addedTraits: string[];
    removedTraits: string[];
    changedTraits: string[];
};

export type WorldSnapshotDiff = {
    added: number[];
    removed: number[];
    changed: number[];
};

export type TraitRegistryEntry = [string, Trait | Relation<Trait>];

type RegistryData = {
    byKey: Map<string, Trait | Relation<Trait>>;
    keyByTrait: Map<Trait, string>;
    keyByRelation: Map<Relation<Trait>, string>;
};

const $registry = Symbol('koota.traitRegistry');

export type TraitRegistry = {
    [$registry]: RegistryData;
};

function registryData(registry: TraitRegistry): RegistryData {
    const data = registry?.[$registry];
    if (!data) throw new Error('Koota: Invalid trait registry.');
    return data;
}

export function createTraitRegistry(...entries: TraitRegistryEntry[]): TraitRegistry {
    const byKey = new Map<string, Trait | Relation<Trait>>();
    const keyByTrait = new Map<Trait, string>();
    const keyByRelation = new Map<Relation<Trait>, string>();

    for (const [key, value] of entries) {
        if (byKey.has(key)) throw new Error(`Koota: Duplicate registry key "${key}".`);

        if (isRelation(value)) {
            if (keyByRelation.has(value)) {
                throw new Error(`Koota: Duplicate relation in registry "${key}".`);
            }
            keyByRelation.set(value, key);
        } else {
            if (keyByTrait.has(value)) {
                throw new Error(`Koota: Duplicate trait in registry "${key}".`);
            }
            keyByTrait.set(value, key);
        }

        byKey.set(key, value);
    }

    return { [$registry]: { byKey, keyByTrait, keyByRelation } };
}

function deepCopy<T>(value: T): T {
    if (typeof value !== 'object' || value === null) return value;
    if (Array.isArray(value)) return value.map((item) => deepCopy(item)) as T;

    const copy = Object.create(Object.getPrototypeOf(value));
    for (const key of Object.keys(value as object)) {
        copy[key] = deepCopy((value as Record<string, unknown>)[key]);
    }
    return copy;
}

function assertEntityAlive(world: World, entity: Entity, action: string) {
    if (!world.has(entity)) {
        throw new Error(`Koota: Cannot ${action} a destroyed entity.`);
    }
}

function entityById(world: World, id: number): Entity | undefined {
    const index = world[$internal].entityIndex;
    const denseIndex = index.sparse[id];
    if (denseIndex === undefined || denseIndex >= index.aliveCount) return undefined;
    const entity = index.dense[denseIndex];
    if (getEntityId(entity) !== id) return undefined;
    return entity;
}

function relationHasStore(relation: Relation<Trait>): boolean {
    return relation[$internal].trait[$internal].type !== 'tag';
}

export function snapshotEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry
): EntitySnapshot {
    assertEntityAlive(world, entity, 'snapshot');
    const { keyByTrait, keyByRelation } = registryData(registry);
    const traits: Record<string, object | true> = {};
    const relations: Record<string, RelationTargetSnapshot[]> = {};
    const entityTraits = world[$internal].entityTraits.get(entity);

    if (entityTraits) {
        for (const trait of entityTraits) {
            const relation = trait[$internal].relation;
            if (relation) {
                const key = keyByRelation.get(relation);
                if (!key) throw new Error('Koota: Unregistered relation.');

                const targets = getRelationTargets(world, relation, entity);
                const hasStore = relationHasStore(relation);
                relations[key] = targets.map((target) => {
                    const entry: RelationTargetSnapshot = { targetId: getEntityId(target) };
                    if (hasStore) {
                        entry.data = deepCopy(getRelationData(world, entity, relation, target) as object);
                    }
                    return entry;
                });
                continue;
            }

            const key = keyByTrait.get(trait);
            if (!key) throw new Error('Koota: Unregistered trait.');

            if (trait[$internal].type === 'tag') {
                traits[key] = true;
            } else {
                traits[key] = deepCopy(getTrait(world, entity, trait) as object);
            }
        }
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

function resolveTarget(world: World, targetId: number, dangling: boolean): Entity {
    const target = entityById(world, targetId);
    if (!target) {
        throw new Error(
            dangling
                ? `Koota: Dangling relation target ${targetId}.`
                : `Koota: Relation target entity ${targetId} does not exist.`
        );
    }
    return target;
}

export function rollbackEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
): void {
    assertEntityAlive(world, entity, 'rollback');
    const { byKey } = registryData(registry);

    const desiredTraits = new Map<Trait, object | true>();
    for (const key of Object.keys(snapshot.traits)) {
        const value = byKey.get(key);
        if (!value || isRelation(value)) throw new Error(`Koota: Unknown registry key "${key}".`);
        desiredTraits.set(value, snapshot.traits[key]);
    }

    const desiredRelations = new Map<Relation<Trait>, RelationTargetSnapshot[]>();
    if (snapshot.relations) {
        for (const key of Object.keys(snapshot.relations)) {
            const value = byKey.get(key);
            if (!value || !isRelation(value)) throw new Error(`Koota: Unknown registry key "${key}".`);
            const targets = snapshot.relations[key];
            for (const target of targets) resolveTarget(world, target.targetId, false);
            desiredRelations.set(value, targets);
        }
    }

    const current = world[$internal].entityTraits.get(entity);
    if (current) {
        const currentTraits: Trait[] = [];
        for (const trait of current) currentTraits.push(trait);
        for (const trait of currentTraits) {
            const relation = trait[$internal].relation;
            if (relation) {
                const keep = desiredRelations.get(relation);
                if (!keep) {
                    removeTrait(world, entity, trait);
                    continue;
                }
                const keepIds = new Set(keep.map((entry) => entry.targetId));
                for (const target of getRelationTargets(world, relation, entity)) {
                    if (!keepIds.has(getEntityId(target))) {
                        removeTrait(world, entity, relation(target));
                    }
                }
                continue;
            }

            if (!desiredTraits.has(trait)) removeTrait(world, entity, trait);
        }
    }

    for (const [trait, value] of desiredTraits) {
        if (!hasTrait(world, entity, trait)) addTrait(world, entity, trait);
        if (value !== true && trait[$internal].type !== 'tag') {
            setTrait(world, entity, trait, deepCopy(value), true);
        }
    }

    for (const [relation, targets] of desiredRelations) {
        const hasStore = relationHasStore(relation);
        for (const entry of targets) {
            const target = resolveTarget(world, entry.targetId, false);
            if (!hasTrait(world, entity, relation[$internal].trait)) {
                addTrait(world, entity, relation(target));
            } else {
                const existing = getRelationTargets(world, relation, entity);
                const already = existing.some((candidate) => getEntityId(candidate) === entry.targetId);
                if (!already) addTrait(world, entity, relation(target));
            }
            if (hasStore && entry.data) {
                setTrait(world, entity, relation(target), deepCopy(entry.data), true);
            }
        }
    }
}

export function rollbackWorld(world: World, registry: TraitRegistry, checkpoint: WorldSnapshot): void {
    if (checkpoint == null || !Array.isArray(checkpoint.entities)) {
        throw new Error('Koota: World snapshot is missing an entities array.');
    }

    const { byKey } = registryData(registry);
    const ids = new Set<number>();

    for (const snapshot of checkpoint.entities) {
        ids.add(snapshot.id);
        for (const key of Object.keys(snapshot.traits)) {
            const value = byKey.get(key);
            if (!value || isRelation(value)) throw new Error(`Koota: Unknown registry key "${key}".`);
        }
        if (!snapshot.relations) continue;
        for (const key of Object.keys(snapshot.relations)) {
            const value = byKey.get(key);
            if (!value || !isRelation(value)) throw new Error(`Koota: Unknown registry key "${key}".`);
        }
    }

    for (const snapshot of checkpoint.entities) {
        if (!snapshot.relations) continue;
        for (const targets of Object.values(snapshot.relations)) {
            for (const target of targets) {
                // The internal world entity is recreated as id 0 and is a valid target.
                if (!ids.has(target.targetId) && target.targetId !== 0) {
                    throw new Error(`Koota: Dangling relation target ${target.targetId}.`);
                }
            }
        }
    }

    world.reset();

    const worldEntityId = getEntityId(world[$internal].worldEntity);
    for (const snapshot of checkpoint.entities) {
        if (snapshot.id === worldEntityId) continue;
        const ctx = world[$internal];
        const entity = allocateEntityWithId(ctx.entityIndex, snapshot.id);
        for (const query of ctx.notQueries) {
            const match = query.check(world, entity);
            if (match) query.add(entity);
            query.resetTrackingBitmasks(snapshot.id);
        }
        if (!ctx.entityTraits.has(entity)) ctx.entityTraits.set(entity, new Set());
    }

    for (const snapshot of checkpoint.entities) {
        const entity =
            snapshot.id === worldEntityId
                ? world[$internal].worldEntity
                : entityById(world, snapshot.id);
        if (!entity) throw new Error(`Koota: Failed to recreate entity ${snapshot.id}.`);
        rollbackEntity(world, entity, registry, snapshot);
    }
}

function assertEntitySnapshot(snapshot: EntitySnapshot, label: string) {
    if (snapshot == null) throw new Error(`Koota: Entity snapshot ${label} is null or undefined.`);
}

function traitValueEqual(a: object | true, b: object | true): boolean {
    if (a === true || b === true) return a === b;
    return shallowEqual(a, b);
}

export function diffEntitySnapshots(a: EntitySnapshot, b: EntitySnapshot): EntitySnapshotDiff {
    assertEntitySnapshot(a, 'a');
    assertEntitySnapshot(b, 'b');

    const aTraits = a.traits ?? {};
    const bTraits = b.traits ?? {};
    const addedTraits: string[] = [];
    const removedTraits: string[] = [];
    const changedTraits: string[] = [];

    for (const key of Object.keys(aTraits)) {
        if (!Object.hasOwn(bTraits, key)) removedTraits.push(key);
        else if (!traitValueEqual(aTraits[key], bTraits[key])) changedTraits.push(key);
    }
    for (const key of Object.keys(bTraits)) {
        if (!Object.hasOwn(aTraits, key)) addedTraits.push(key);
    }

    addedTraits.sort();
    removedTraits.sort();
    changedTraits.sort();

    return { addedTraits, removedTraits, changedTraits };
}

function relationTargetsEqual(a: RelationTargetSnapshot[], b: RelationTargetSnapshot[]): boolean {
    if (a.length !== b.length) return false;
    const sort = (list: RelationTargetSnapshot[]) =>
        [...list].sort((left, right) => left.targetId - right.targetId || (left.data ? 1 : 0) - (right.data ? 1 : 0));
    const left = sort(a);
    const right = sort(b);
    for (let i = 0; i < left.length; i++) {
        if (left[i].targetId !== right[i].targetId) return false;
        const leftData = left[i].data;
        const rightData = right[i].data;
        if (leftData === undefined && rightData === undefined) continue;
        if (leftData === undefined || rightData === undefined) return false;
        if (!shallowEqual(leftData, rightData)) return false;
    }
    return true;
}

function entitySnapshotsEqual(a: EntitySnapshot, b: EntitySnapshot): boolean {
    const aTraitKeys = Object.keys(a.traits);
    const bTraitKeys = Object.keys(b.traits);
    if (aTraitKeys.length !== bTraitKeys.length) return false;
    for (const key of aTraitKeys) {
        if (!Object.hasOwn(b.traits, key)) return false;
        if (!traitValueEqual(a.traits[key], b.traits[key])) return false;
    }

    const aRelations = a.relations ?? {};
    const bRelations = b.relations ?? {};
    const aRelKeys = Object.keys(aRelations);
    const bRelKeys = Object.keys(bRelations);
    if (aRelKeys.length !== bRelKeys.length) return false;
    for (const key of aRelKeys) {
        if (!Object.hasOwn(bRelations, key)) return false;
        if (!relationTargetsEqual(aRelations[key], bRelations[key])) return false;
    }
    return true;
}

export function diffWorldSnapshots(before: WorldSnapshot, after: WorldSnapshot): WorldSnapshotDiff {
    if (before == null || !Array.isArray(before.entities)) {
        throw new Error('Koota: World snapshot is missing an entities array.');
    }
    if (after == null || !Array.isArray(after.entities)) {
        throw new Error('Koota: World snapshot is missing an entities array.');
    }

    const beforeMap = new Map<number, EntitySnapshot>();
    const afterMap = new Map<number, EntitySnapshot>();
    for (const entity of before.entities) beforeMap.set(entity.id, entity);
    for (const entity of after.entities) afterMap.set(entity.id, entity);

    const added: number[] = [];
    const removed: number[] = [];
    const changed: number[] = [];

    for (const [id, snapshot] of beforeMap) {
        const next = afterMap.get(id);
        if (!next) removed.push(id);
        else if (!entitySnapshotsEqual(snapshot, next)) changed.push(id);
    }
    for (const id of afterMap.keys()) {
        if (!beforeMap.has(id)) added.push(id);
    }

    added.sort((a, b) => a - b);
    removed.sort((a, b) => a - b);
    changed.sort((a, b) => a - b);

    return { added, removed, changed };
}

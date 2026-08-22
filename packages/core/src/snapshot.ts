import { $internal } from './common';
import { createEntityWithId } from './entity/entity';
import type { Entity } from './entity/types';
import { getEntityId, packEntity } from './entity/utils/pack-entity';
import { getRelationData, getRelationTargets } from './relation/relation';
import type { Relation } from './relation/types';
import { isRelation } from './relation/utils/is-relation';
import { getStore } from './trait/trait';
import type { Trait } from './trait/types';
import { addTrait, hasTrait, removeTrait } from './trait/trait';
import type { World } from './world';

export type TraitOrRelation = Trait | Relation;
export type TraitRegistryEntry = [string, TraitOrRelation];

export type TraitRegistry = {
    entries: readonly TraitRegistryEntry[];
    traits: ReadonlyMap<string, Trait>;
    relations: ReadonlyMap<string, Relation>;
    keys: ReadonlyMap<TraitOrRelation, string>;
};

export type EntitySnapshot = {
    id: number;
    traits: Record<string, object | true>;
    relations?: Record<string, Array<{ targetId: number; data?: object }>>;
};

export type WorldSnapshot = {
    entities: EntitySnapshot[];
};

const clone = <T>(value: T): T => structuredClone(value);

export function createTraitRegistry(...entries: TraitRegistryEntry[]): TraitRegistry {
    const traits = new Map<string, Trait>();
    const relations = new Map<string, Relation>();
    const keys = new Map<TraitOrRelation, string>();

    for (const [key, value] of entries) {
        if (traits.has(key) || relations.has(key)) throw new Error(`Duplicate registry key: ${key}`);
        if (keys.has(value)) {
            throw new Error(`Duplicate registry value: ${key}`);
        }

        keys.set(value, key);
        if (isRelation(value)) relations.set(key, value);
        else traits.set(key, value);
    }

    return { entries: entries.slice(), traits, relations, keys };
}

function requireRegistryValue(registry: TraitRegistry, key: string): TraitOrRelation {
    const value = registry.traits.get(key) ?? registry.relations.get(key);
    if (!value) throw new Error(`Unknown registry key: ${key}`);
    return value;
}

function requireEntity(world: World, entity: Entity): void {
    if (!world.has(entity)) throw new Error('Cannot snapshot or rollback a destroyed entity.');
}

function entityForId(world: World, id: number): Entity {
    return packEntity(world.id, 0, id);
}

function snapshotTrait(world: World, entity: Entity, trait: Trait): object | true | undefined {
    if (!hasTrait(world, entity, trait)) return undefined;
    if (trait[$internal].type === 'tag') return true;
    return clone(
        getStore(world, trait) && trait[$internal].get(getEntityId(entity), getStore(world, trait))
    );
}

export function snapshotEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry
): EntitySnapshot {
    requireEntity(world, entity);
    for (const trait of world[$internal].entityTraits.get(entity) ?? []) {
        const key = registry.keys.get(trait[$internal].relation ?? trait);
        if (!key) throw new Error('Entity has an unregistered trait or relation.');
    }
    const traits: Record<string, object | true> = {};
    const relations: Record<string, Array<{ targetId: number; data?: object }>> = {};

    for (const [key, value] of registry.entries) {
        if (isRelation(value)) {
            const targets = getRelationTargets(world, value, entity);
            if (targets.length > 0) {
                relations[key] = targets.map((target) => {
                    const data = getRelationData(world, entity, value, target);
                    return value[$internal].trait[$internal].type === 'tag'
                        ? { targetId: getEntityId(target) }
                        : data === undefined
                          ? { targetId: getEntityId(target) }
                          : { targetId: getEntityId(target), data: clone(data as object) };
                });
            }
        } else {
            const data = snapshotTrait(world, entity, value);
            if (data !== undefined) traits[key] = data;
        }
    }

    const result: EntitySnapshot = { id: getEntityId(entity), traits };
    if (Object.keys(relations).length > 0) result.relations = relations;
    return result;
}

export function snapshotWorld(world: World, registry: TraitRegistry): WorldSnapshot {
    const worldEntity = world[$internal].worldEntity;
    return {
        entities: world.entities
            .filter((entity) => entity !== worldEntity)
            .map((entity) => snapshotEntity(world, entity, registry)),
    };
}

function validateSnapshot(snapshot: EntitySnapshot, registry: TraitRegistry): void {
    for (const key of Object.keys(snapshot.traits)) {
        const value = requireRegistryValue(registry, key);
        if (isRelation(value)) throw new Error(`Registry key is not a trait: ${key}`);
    }
    for (const key of Object.keys(snapshot.relations ?? {})) {
        const value = requireRegistryValue(registry, key);
        if (!isRelation(value)) throw new Error(`Registry key is not a relation: ${key}`);
    }
}

export function rollbackEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
): void {
    requireEntity(world, entity);
    validateSnapshot(snapshot, registry);
    for (const entries of Object.values(snapshot.relations ?? {})) {
        for (const entry of entries) {
            if (!world.has(entityForId(world, entry.targetId))) {
                throw new Error(`Relation target does not exist: ${entry.targetId}`);
            }
        }
    }

    for (const trait of Array.from(world[$internal].entityTraits.get(entity) ?? [])) {
        const registryValue = trait[$internal].relation ?? trait;
        const key = registry.keys.get(registryValue);
        if (!key) throw new Error('Entity has an unregistered trait or relation.');
        if (!Object.hasOwn(snapshot.traits, key) && !Object.hasOwn(snapshot.relations ?? {}, key)) {
            removeTrait(
                world,
                entity,
                trait[$internal].relation ? trait[$internal].relation('*') : trait
            );
        }
    }
    for (const [key, relation] of registry.relations) {
        const expected = snapshot.relations?.[key] ?? [];
        for (const target of getRelationTargets(world, relation, entity)) {
            if (!expected.some((entry) => entry.targetId === getEntityId(target))) {
                removeTrait(world, entity, relation(target));
            }
        }
    }

    for (const [key, value] of Object.entries(snapshot.traits)) {
        const trait = registry.traits.get(key);
        if (!trait) continue;
        if (value === true) addTrait(world, entity, trait);
        else if (hasTrait(world, entity, trait)) entity.set(trait, clone(value));
        else addTrait(world, entity, [trait, clone(value)] as never);
    }

    for (const [key, entries] of Object.entries(snapshot.relations ?? {})) {
        const relation = registry.relations.get(key)!;
        for (const entry of entries) {
            const target = entityForId(world, entry.targetId);
            if (entry.data !== undefined && hasRelationTarget(world, relation, entity, target)) {
                entity.set(relation(target), clone(entry.data) as Record<string, unknown>);
            } else {
                addTrait(
                    world,
                    entity,
                    relation(
                        target,
                        entry.data !== undefined
                            ? (clone(entry.data) as Record<string, unknown>)
                            : undefined
                    )
                );
            }
        }
    }
}

function hasRelationTarget(
    world: World,
    relation: Relation,
    entity: Entity,
    target: Entity
): boolean {
    return getRelationTargets(world, relation, entity).some((current) => current === target);
}

export function rollbackWorld(
    world: World,
    registry: TraitRegistry,
    checkpoint: WorldSnapshot
): void {
    if (!checkpoint || !Array.isArray(checkpoint.entities)) {
        throw new Error('Invalid world snapshot.');
    }
    for (const snapshot of checkpoint.entities) validateSnapshot(snapshot, registry);
    const ids = new Set(checkpoint.entities.map((snapshot) => snapshot.id));
    for (const snapshot of checkpoint.entities) {
        for (const entries of Object.values(snapshot.relations ?? {})) {
            for (const entry of entries) {
                if (!ids.has(entry.targetId)) {
                    throw new Error(`Relation target does not exist: ${entry.targetId}`);
                }
            }
        }
    }

    world.reset();
    const entities = new Map<number, Entity>();
    for (const snapshot of checkpoint.entities.slice().sort((a, b) => a.id - b.id)) {
        entities.set(snapshot.id, createEntityWithId(world, snapshot.id));
    }
    for (const snapshot of checkpoint.entities) {
        rollbackEntity(world, entities.get(snapshot.id)!, registry, snapshot);
    }
}

function equalData(a: unknown, b: unknown): boolean {
    if (a === b) return true;
    if (!a || !b || typeof a !== 'object' || typeof b !== 'object') return false;
    const ak = Object.keys(a);
    const bk = Object.keys(b);
    return (
        ak.length === bk.length &&
        ak.every((key) => Object.hasOwn(b as object, key) && (a as any)[key] === (b as any)[key])
    );
}

export function diffEntitySnapshots(a: EntitySnapshot, b: EntitySnapshot) {
    if (!a || !b) throw new Error('Snapshots cannot be null or undefined.');
    const addedTraits: string[] = [];
    const removedTraits: string[] = [];
    const changedTraits: string[] = [];
    for (const key of Object.keys(b.traits)) {
        if (!Object.hasOwn(a.traits, key)) addedTraits.push(key);
        else if (!equalData(a.traits[key], b.traits[key])) changedTraits.push(key);
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

function entityEqual(a: EntitySnapshot, b: EntitySnapshot): boolean {
    const traitKeys = new Set([...Object.keys(a.traits), ...Object.keys(b.traits)]);
    for (const key of traitKeys) {
        if (
            !Object.hasOwn(a.traits, key) ||
            !Object.hasOwn(b.traits, key) ||
            !equalData(a.traits[key], b.traits[key])
        )
            return false;
    }
    const relationsA = a.relations ?? {};
    const relationsB = b.relations ?? {};
    const relationKeys = new Set([...Object.keys(relationsA), ...Object.keys(relationsB)]);
    for (const key of relationKeys) {
        const pairsA = relationsA[key] ?? [];
        const pairsB = relationsB[key] ?? [];
        if (pairsA.length !== pairsB.length) return false;
        const sortedA = pairsA.slice().sort((x, y) => x.targetId - y.targetId);
        const sortedB = pairsB.slice().sort((x, y) => x.targetId - y.targetId);
        if (
            sortedA.some(
                (pair, i) =>
                    pair.targetId !== sortedB[i].targetId || !equalData(pair.data, sortedB[i].data)
            )
        )
            return false;
    }
    return true;
}

export function diffWorldSnapshots(before: WorldSnapshot, after: WorldSnapshot) {
    if (!before || !after || !Array.isArray(before.entities) || !Array.isArray(after.entities)) {
        throw new Error('Invalid world snapshot.');
    }
    const previous = new Map(before.entities.map((entity) => [entity.id, entity]));
    const current = new Map(after.entities.map((entity) => [entity.id, entity]));
    const added = [...current.keys()].filter((id) => !previous.has(id)).sort((a, b) => a - b);
    const removed = [...previous.keys()].filter((id) => !current.has(id)).sort((a, b) => a - b);
    const changed = [...current.keys()]
        .filter((id) => previous.has(id) && !entityEqual(previous.get(id)!, current.get(id)!))
        .sort((a, b) => a - b);
    return { added, removed, changed };
}

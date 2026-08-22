import { $internal } from './common';
import { createEntity, destroyEntity } from './entity/entity';
import type { Entity } from './entity/types';
import { getEntityId, packEntity } from './entity/utils/pack-entity';
import { getRelationData, getRelationTargets } from './relation/relation';
import type { Relation } from './relation/types';
import { isRelation } from './relation/utils/is-relation';
import { addTrait, getTrait, hasTrait, removeTrait } from './trait/trait';
import type { Trait } from './trait/types';
import type { World } from './world';

export type EntitySnapshot = {
    id: number;
    traits: Record<string, object | true>;
    relations?: Record<string, Array<{ targetId: number; data?: object }>>;
};
export type WorldSnapshot = { entities: EntitySnapshot[] };
export type TraitRegistry = {
    traits: Map<string, Trait>;
    relations: Map<string, Relation>;
};

const clone = <T>(value: T): T => {
    if (value === undefined || value === null || typeof value !== 'object') return value;
    if (typeof structuredClone === 'function') return structuredClone(value);
    if (Array.isArray(value)) return value.map(clone) as T;
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, clone(item)])) as T;
};

export function createTraitRegistry(
    ...entries: Array<[string, Trait | Relation]>
): TraitRegistry {
    const traits = new Map<string, Trait>();
    const relations = new Map<string, Relation>();
    const seenTraits = new Set<Trait>();
    const seenRelations = new Set<Relation>();
    for (const [key, value] of entries) {
        if (traits.has(key) || relations.has(key)) throw new Error(`Duplicate registry key: ${key}`);
        const relation = isRelation(value);
        const seen = relation ? seenRelations : seenTraits;
        if (seen.has(value)) throw new Error(`Duplicate ${relation ? 'relation' : 'trait'}`);
        seen.add(value);
        (relation ? relations : traits).set(key, value);
    }
    return { traits, relations };
}

function validateEntity(world: World, entity: Entity) {
    if (!world.has(entity)) throw new Error('Cannot snapshot or rollback a destroyed entity.');
}

function keyFor(registry: TraitRegistry, value: Trait | Relation): string | undefined {
    for (const [key, candidate] of [...registry.traits, ...registry.relations])
        if (candidate === value) return key;
    return undefined;
}

export function snapshotEntity(world: World, entity: Entity, registry: TraitRegistry): EntitySnapshot {
    validateEntity(world, entity);
    const traits: Record<string, object | true> = {};
    const relations: Record<string, Array<{ targetId: number; data?: object }>> = {};
    for (const [key, trait] of registry.traits) {
        if (hasTrait(world, entity, trait))
            traits[key] = trait[$internal].type === 'tag' ? true : clone(getTrait(world, entity, trait)!);
    }
    for (const [key, relation] of registry.relations) {
        const targets = getRelationTargets(world, relation, entity);
        if (targets.length)
            relations[key] = targets.map((target) => {
                const data = getRelationData(world, entity, relation, target);
                return data && Object.keys(data).length ? { targetId: getEntityId(target), data: clone(data) } : { targetId: getEntityId(target) };
            });
    }
    const result: EntitySnapshot = { id: getEntityId(entity), traits };
    if (Object.keys(relations).length) result.relations = relations;
    return result;
}

export function snapshotWorld(world: World, registry: TraitRegistry): WorldSnapshot {
    return { entities: world.entities.filter((entity) => entity !== world[$internal].worldEntity).map((entity) => snapshotEntity(world, entity, registry)) };
}

function resolve(registry: TraitRegistry, key: string): Trait | Relation {
    const value = registry.traits.get(key) ?? registry.relations.get(key);
    if (!value) throw new Error(`Unknown registry key: ${key}`);
    return value;
}

export function rollbackEntity(world: World, entity: Entity, registry: TraitRegistry, snapshot: EntitySnapshot) {
    validateEntity(world, entity);
    for (const key of Object.keys(snapshot.traits)) resolve(registry, key);
    for (const key of Object.keys(snapshot.relations ?? {})) {
        if (!registry.relations.has(key)) throw new Error(`Unknown registry key: ${key}`);
        for (const target of snapshot.relations![key]) if (!world.has(packEntity(world.id, 0, target.targetId) as Entity))
            throw new Error(`Dangling relation target: ${target.targetId}`);
    }
    for (const [key, trait] of registry.traits) if (hasTrait(world, entity, trait) && !(key in snapshot.traits)) removeTrait(world, entity, trait);
    for (const [key, relation] of registry.relations) {
        const desired = snapshot.relations?.[key] ?? [];
        const pairTargets = getRelationTargets(world, relation, entity);
        for (const target of pairTargets) if (!desired.some((item) => item.targetId === getEntityId(target))) entity.remove(relation(target));
    }
    for (const [key, value] of Object.entries(snapshot.traits)) {
        const trait = registry.traits.get(key);
        if (!trait) throw new Error(`Registry key is a relation, not a trait: ${key}`);
        if (!hasTrait(world, entity, trait)) addTrait(world, entity, value === true ? trait : [trait, value] as never);
        else if (value !== true) entity.set(trait, clone(value));
    }
    for (const [key, targets] of Object.entries(snapshot.relations ?? {})) {
        const relation = registry.relations.get(key)!;
        for (const target of targets) {
            const targetEntity = packEntity(world.id, 0, target.targetId) as Entity;
            if (!entity.has(relation(targetEntity))) entity.add(relation(targetEntity));
            if (target.data) entity.set(relation(targetEntity), clone(target.data));
        }
    }
}

export function rollbackWorld(world: World, registry: TraitRegistry, checkpoint: WorldSnapshot) {
    if (!checkpoint || !Array.isArray(checkpoint.entities)) throw new Error('Invalid world snapshot');
    for (const snapshot of checkpoint.entities) for (const key of Object.keys(snapshot.traits)) resolve(registry, key);
    for (const snapshot of checkpoint.entities) for (const targets of Object.values(snapshot.relations ?? {}))
        for (const target of targets) if (!checkpoint.entities.some((item) => item.id === target.targetId)) throw new Error(`Dangling relation target: ${target.targetId}`);
    world.reset();
    for (const entity of [...world.entities]) if (entity !== world[$internal].worldEntity) destroyEntity(world, entity);
    for (const snapshot of checkpoint.entities) {
        const entity = createEntity(world);
        (world[$internal].entityIndex.dense[world[$internal].entityIndex.aliveCount - 1] as number) = packEntity(world.id, 0, snapshot.id);
        rollbackEntity(world, entity, registry, snapshot);
    }
}

const shallowEqual = (a: object | true | undefined, b: object | true | undefined) =>
    a === b || (a !== undefined && b !== undefined && a !== true && b !== true && Object.keys(a).length === Object.keys(b).length && Object.keys(a).every((key) => a[key as keyof typeof a] === b[key as keyof typeof b]));

export function diffEntitySnapshots(a: EntitySnapshot, b: EntitySnapshot) {
    if (!a || !b) throw new Error('Snapshots are required');
    const addedTraits = Object.keys(b.traits).filter((key) => !(key in a.traits)).sort();
    const removedTraits = Object.keys(a.traits).filter((key) => !(key in b.traits)).sort();
    const changedTraits = Object.keys(a.traits).filter((key) => key in b.traits && !shallowEqual(a.traits[key], b.traits[key])).sort();
    return { addedTraits, removedTraits, changedTraits };
}

export function diffWorldSnapshots(before: WorldSnapshot, after: WorldSnapshot) {
    if (!before || !Array.isArray(before.entities) || !after || !Array.isArray(after.entities)) throw new Error('World snapshots must contain entities');
    const a = new Map(before.entities.map((entity) => [entity.id, entity]));
    const b = new Map(after.entities.map((entity) => [entity.id, entity]));
    const added = [...b.keys()].filter((id) => !a.has(id)).sort((x, y) => x - y);
    const removed = [...a.keys()].filter((id) => !b.has(id)).sort((x, y) => x - y);
    const changed = [...a.keys()].filter((id) => b.has(id) && JSON.stringify(a.get(id)) !== JSON.stringify(b.get(id))).sort((x, y) => x - y);
    return { added, removed, changed };
}

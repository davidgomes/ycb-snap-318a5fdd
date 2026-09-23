import { $internal } from '../common';
import { createEntityAt, destroyEntity } from '../entity/entity';
import type { Entity } from '../entity/types';
import { getEntityGeneration, getEntityId, packEntity } from '../entity/utils/pack-entity';
import { setChanged } from '../query/modifiers/changed';
import { getOrderedTraitRelation, isOrderedTrait } from '../relation/ordered';
import type { OrderedList } from '../relation/ordered-list';
import {
    getEntitiesWithRelationTo,
    getRelationData,
    getRelationTargets,
    hasRelationToTarget,
} from '../relation/relation';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import type { Trait } from '../trait/types';
import { deepClone } from '../utils/deep-clone';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world';
import type { EntitySnapshot, RelationTargetSnapshot, TraitRegistry, WorldSnapshot } from './types';

type ResolvedSnapshot = {
    id: number;
    traits: Map<Trait, object | true>;
    relations: Map<Relation<Trait>, RelationTargetSnapshot[]>;
};

// Snapshot IDs keep the entity ID and generation but not the world, so a checkpoint
// revives the exact same entities in its own world and can be restored into any other.
function toSnapshotId(entity: Entity): number {
    return packEntity(0, getEntityGeneration(entity), getEntityId(entity));
}

function fromSnapshotId(world: World, id: number): Entity {
    return packEntity(world.id, getEntityGeneration(id as Entity), getEntityId(id as Entity));
}

function isSnapshotId(id: unknown): id is number {
    return Number.isInteger(id) && (id as number) >= 0 && toSnapshotId(id as Entity) === id;
}

function getRegistry(registry: TraitRegistry) {
    const ctx = registry?.[$internal];
    if (!ctx) throw new Error('Koota: Expected a trait registry created with createTraitRegistry.');
    return ctx;
}

export function snapshotEntity(world: World, entity: Entity, registry: TraitRegistry): EntitySnapshot {
    if (!world.has(entity)) throw new Error('Koota: Cannot snapshot an entity that does not exist.');

    const { keys } = getRegistry(registry);
    const id = toSnapshotId(entity);
    const traits: EntitySnapshot['traits'] = {};
    const relations: NonNullable<EntitySnapshot['relations']> = {};
    let hasRelations = false;

    for (const trait of world[$internal].entityTraits.get(entity)!) {
        const relation = trait[$internal].relation as Relation<Trait> | null;
        const key = keys.get(relation ?? trait);

        if (key === undefined) {
            const kind = relation ? 'relation' : 'trait';
            throw new Error(`Koota: Entity ${id} has a ${kind} that is not in the trait registry.`);
        }

        if (!relation) {
            traits[key] = snapshotTrait(world, entity, trait);
            continue;
        }

        const targets = getRelationTargets(world, relation, entity);
        if (targets.length === 0) continue;

        const hasStore = trait[$internal].type !== 'tag';
        relations[key] = targets.map((target) =>
            hasStore
                ? {
                      targetId: toSnapshotId(target),
                      data: deepClone(getRelationData(world, entity, relation, target) as object),
                  }
                : { targetId: toSnapshotId(target) }
        );
        hasRelations = true;
    }

    const snapshot: EntitySnapshot = { id, traits };
    if (hasRelations) snapshot.relations = relations;

    return snapshot;
}

function snapshotTrait(world: World, entity: Entity, trait: Trait): object | true {
    if (trait[$internal].type === 'tag') return true;

    const value = getTrait(world, entity, trait);

    // Ordered lists mirror relations held by other entities, so only their order is stored.
    if (isOrderedTrait(trait)) return (value as OrderedList).map(toSnapshotId);

    return deepClone(value as object);
}

export function snapshotWorld(world: World, registry: TraitRegistry): WorldSnapshot {
    const worldEntity = world[$internal].worldEntity;
    const entities: EntitySnapshot[] = [];

    for (const entity of world.entities) {
        if (entity !== worldEntity) entities.push(snapshotEntity(world, entity, registry));
    }

    return { entities };
}

export function rollbackEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
): void {
    if (!world.has(entity)) throw new Error('Koota: Cannot roll back an entity that does not exist.');

    const resolved = resolveSnapshot(registry, snapshot);

    for (const targets of resolved.relations.values()) {
        for (const { targetId } of targets) {
            if (!world.has(fromSnapshotId(world, targetId))) {
                throw new Error(`Koota: Relation target ${targetId} does not exist in the world.`);
            }
        }
    }

    applySnapshot(world, entity, resolved);
    restoreOrderedTraits(world, entity, resolved);
}

export function rollbackWorld(world: World, registry: TraitRegistry, checkpoint: WorldSnapshot): void {
    if (!Array.isArray(checkpoint?.entities)) {
        throw new Error('Koota: A world checkpoint must have an entities array.');
    }

    const worldEntity = world[$internal].worldEntity;
    const resolved = checkpoint.entities.map((snapshot) => resolveSnapshot(registry, snapshot));
    const ids = new Set<number>([toSnapshotId(worldEntity)]);
    const usedEntityIds = new Set<number>();

    // Validate the whole checkpoint before touching the world so a bad one leaves it intact.
    for (const { id } of resolved) {
        if (!isSnapshotId(id)) throw new Error(`Koota: Invalid entity ID ${id} in checkpoint.`);

        const entityId = getEntityId(id as Entity);
        if (entityId === getEntityId(worldEntity)) {
            throw new Error(`Koota: Entity ID ${id} in checkpoint is reserved by the world entity.`);
        }
        if (usedEntityIds.has(entityId)) {
            throw new Error(`Koota: Entity ID ${id} appears more than once in checkpoint.`);
        }

        usedEntityIds.add(entityId);
        ids.add(id);
    }

    for (const { relations } of resolved) {
        for (const targets of relations.values()) {
            for (const { targetId } of targets) {
                if (!ids.has(targetId)) {
                    throw new Error(`Koota: Relation target ${targetId} does not exist in checkpoint.`);
                }
            }
        }
    }

    for (const entity of world.entities) {
        if (entity !== worldEntity && world.has(entity)) destroyEntity(world, entity);
    }

    // Create every entity before applying traits so relation targets exist.
    const entities = resolved.map(({ id }) =>
        createEntityAt(world, getEntityId(id as Entity), getEntityGeneration(id as Entity))
    );

    for (let i = 0; i < entities.length; i++) applySnapshot(world, entities[i], resolved[i]);
    for (let i = 0; i < entities.length; i++) restoreOrderedTraits(world, entities[i], resolved[i]);
}

function resolveSnapshot(registry: TraitRegistry, snapshot: EntitySnapshot): ResolvedSnapshot {
    const { entries } = getRegistry(registry);

    if (typeof snapshot?.traits !== 'object' || snapshot.traits === null) {
        throw new Error('Koota: An entity snapshot must have a traits object.');
    }

    const traits = new Map<Trait, object | true>();
    for (const key of Object.keys(snapshot.traits)) {
        const trait = entries.get(key);
        if (!trait || isRelation(trait)) throw new Error(`Koota: Unknown trait key "${key}" in snapshot.`);
        traits.set(trait, snapshot.traits[key]);
    }

    const relations = new Map<Relation<Trait>, RelationTargetSnapshot[]>();
    for (const key of Object.keys(snapshot.relations ?? {})) {
        const relation = entries.get(key);
        if (!relation || !isRelation(relation)) {
            throw new Error(`Koota: Unknown relation key "${key}" in snapshot.`);
        }

        const targets = snapshot.relations![key];
        if (!Array.isArray(targets) || !targets.every((target) => isSnapshotId(target?.targetId))) {
            throw new Error(`Koota: Relation "${key}" in snapshot has invalid targets.`);
        }

        relations.set(relation, targets);
    }

    return { id: snapshot.id, traits, relations };
}

/** Tags, `true` markers and ordered lists have no data to write back. */
function hasTraitData(trait: Trait, value: object | true): value is object {
    return value !== true && trait[$internal].type !== 'tag' && !isOrderedTrait(trait);
}

function applySnapshot(world: World, entity: Entity, snapshot: ResolvedSnapshot) {
    for (const trait of world[$internal].entityTraits.get(entity)!) {
        const relation = trait[$internal].relation as Relation<Trait> | null;

        if (!relation) {
            if (!snapshot.traits.has(trait)) removeTrait(world, entity, trait);
            continue;
        }

        const targets = snapshot.relations.get(relation);
        if (!targets) {
            removeTrait(world, entity, trait);
            continue;
        }

        const kept = new Set(targets.map(({ targetId }) => fromSnapshotId(world, targetId)));
        for (const target of getRelationTargets(world, relation, entity)) {
            if (!kept.has(target)) removeTrait(world, entity, relation(target));
        }
    }

    for (const [trait, value] of snapshot.traits) {
        const hasData = hasTraitData(trait, value);

        if (!hasTrait(world, entity, trait)) {
            addTrait(world, entity, hasData ? [trait, deepClone(value)] : trait);
        } else if (hasData && !shallowEqual(getTrait(world, entity, trait), value)) {
            setTrait(world, entity, trait, deepClone(value));
        }
    }

    for (const [relation, targets] of snapshot.relations) {
        const hasStore = relation[$internal].trait[$internal].type !== 'tag';

        for (const { targetId, data } of targets) {
            const target = fromSnapshotId(world, targetId);
            const hasData = hasStore && data !== undefined;

            if (!hasRelationToTarget(world, relation, entity, target)) {
                const params = hasData ? (deepClone(data) as Record<string, unknown>) : undefined;
                addTrait(world, entity, relation(target, params));
            } else if (hasData && !shallowEqual(getRelationData(world, entity, relation, target), data)) {
                setTrait(world, entity, relation(target), deepClone(data));
            }
        }
    }
}

function restoreOrderedTraits(world: World, entity: Entity, snapshot: ResolvedSnapshot) {
    for (const [trait, value] of snapshot.traits) {
        if (!isOrderedTrait(trait) || !Array.isArray(value)) continue;

        const list = getTrait(world, entity, trait) as OrderedList | undefined;
        if (!list) continue;

        // The list must contain exactly the entities related to this one, so only
        // the snapshot's order is applied and unknown members are kept at the end.
        const members = new Set(getEntitiesWithRelationTo(world, getOrderedTraitRelation(trait), entity));
        const order = new Set<Entity>();
        for (const id of value) {
            const member = fromSnapshotId(world, id);
            if (members.has(member)) order.add(member);
        }
        for (const member of list) if (members.has(member)) order.add(member);
        for (const member of members) order.add(member);

        const next = [...order];
        if (next.length === list.length && next.every((member, i) => member === list[i])) continue;

        list.length = 0;
        Array.prototype.push.apply(list, next);
        setChanged(world, entity, trait);
    }
}

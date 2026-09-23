import { $internal } from '../common';
import { createEntityWithId, destroyEntity } from '../entity/entity';
import type { Entity } from '../entity/types';
import { getAliveEntityById, isEntityAlive } from '../entity/utils/entity-index';
import { ENTITY_ID_MASK, getEntityId } from '../entity/utils/pack-entity';
import { IsExcluded } from '../query/query';
import { getRelationData, getRelationTargets, hasRelationToTarget } from '../relation/relation';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import type { Trait } from '../trait/types';
import { deepClone } from '../utils/deep-clone';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world';
import type { EntitySnapshot, RelationTargetSnapshot, TraitRegistry, WorldSnapshot } from './types';

type ResolvedSnapshot = {
    traits: [Trait, object | true][];
    relations: [Relation<Trait>, RelationTargetSnapshot[]][];
};

type ResolvedRelationTargets = [Relation<Trait>, [Entity, object | undefined][]][];

function assertEntityAlive(world: World, entity: Entity, action: string) {
    if (!isEntityAlive(world[$internal].entityIndex, entity)) {
        throw new Error(`Koota: Cannot ${action} an entity that does not exist.`);
    }
}

function isValidEntityId(id: unknown): id is number {
    return typeof id === 'number' && Number.isInteger(id) && id >= 0 && id <= ENTITY_ID_MASK;
}

function assertEntitySnapshot(snapshot: EntitySnapshot) {
    if (snapshot === null || typeof snapshot !== 'object') {
        throw new Error('Koota: Expected an entity snapshot.');
    }
    if (snapshot.traits === null || typeof snapshot.traits !== 'object') {
        throw new Error('Koota: Entity snapshot is missing a traits record.');
    }
    if (
        snapshot.relations !== undefined &&
        (snapshot.relations === null || typeof snapshot.relations !== 'object')
    ) {
        throw new Error('Koota: Entity snapshot relations must be a record.');
    }
}

function isTagTrait(trait: Trait) {
    return trait[$internal].type === 'tag';
}

export function snapshotEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry
): EntitySnapshot {
    assertEntityAlive(world, entity, 'snapshot');

    const entityTraits = world[$internal].entityTraits.get(entity)!;

    for (const trait of entityTraits) {
        if (trait === IsExcluded) continue;
        const relation = trait[$internal].relation;
        if (relation ? !registry.relationKeys.has(relation) : !registry.traitKeys.has(trait)) {
            throw new Error(
                `Koota: Cannot snapshot entity ${getEntityId(entity)} because it has an unregistered ${relation ? 'relation' : 'trait'}.`
            );
        }
    }

    const traits: EntitySnapshot['traits'] = {};
    let relations: EntitySnapshot['relations'];

    for (const [key, value] of registry.byKey) {
        if (isRelation(value)) {
            const relationTrait = value[$internal].trait;
            if (!entityTraits.has(relationTrait)) continue;

            const hasData = !isTagTrait(relationTrait);
            const targets = getRelationTargets(world, value, entity);
            if (targets.length === 0) continue;

            (relations ??= {})[key] = targets.map((target) => {
                const targetSnapshot: RelationTargetSnapshot = { targetId: getEntityId(target) };
                if (hasData) {
                    const data = getRelationData(world, entity, value, target);
                    if (data !== undefined) targetSnapshot.data = deepClone(data as object);
                }
                return targetSnapshot;
            });
        } else {
            if (!entityTraits.has(value)) continue;
            traits[key] = isTagTrait(value)
                ? true
                : deepClone(getTrait(world, entity, value) as object);
        }
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

function resolveSnapshot(registry: TraitRegistry, snapshot: EntitySnapshot): ResolvedSnapshot {
    const traits: ResolvedSnapshot['traits'] = [];
    const relations: ResolvedSnapshot['relations'] = [];

    for (const key of Object.keys(snapshot.traits)) {
        const trait = registry.byKey.get(key);
        if (trait === undefined || isRelation(trait)) {
            throw new Error(`Koota: Unknown trait registry key "${key}" in snapshot.`);
        }
        traits.push([trait, snapshot.traits[key]]);
    }

    if (snapshot.relations) {
        for (const key of Object.keys(snapshot.relations)) {
            const relation = registry.byKey.get(key);
            if (relation === undefined || !isRelation(relation)) {
                throw new Error(`Koota: Unknown relation registry key "${key}" in snapshot.`);
            }

            const targets = snapshot.relations[key];
            if (!Array.isArray(targets)) {
                throw new Error(`Koota: Relation "${key}" in snapshot must be an array of targets.`);
            }
            if (targets.length === 0) continue;
            if (relation[$internal].exclusive && targets.length > 1) {
                throw new Error(
                    `Koota: Exclusive relation "${key}" in snapshot has multiple targets.`
                );
            }
            for (const target of targets) {
                if (
                    target === null ||
                    typeof target !== 'object' ||
                    !isValidEntityId(target.targetId)
                ) {
                    throw new Error(`Koota: Relation "${key}" in snapshot has an invalid target.`);
                }
            }

            relations.push([relation, targets]);
        }
    }

    return { traits, relations };
}

function resolveRelationTargets(
    relations: ResolvedSnapshot['relations'],
    lookup: (id: number) => Entity | undefined
): ResolvedRelationTargets {
    return relations.map(([relation, targets]) => [
        relation,
        targets.map(({ targetId, data }) => {
            const target = lookup(targetId);
            if (target === undefined) {
                throw new Error(
                    `Koota: Cannot roll back relation because target entity ${targetId} does not exist.`
                );
            }
            return [target, data] as [Entity, object | undefined];
        }),
    ]);
}

function removeUnlisted(
    world: World,
    entity: Entity,
    traits: ResolvedSnapshot['traits'],
    relations: ResolvedRelationTargets
) {
    const desiredTraits = new Set(traits.map(([trait]) => trait));
    const desiredRelations = new Map(
        relations.map(([relation, targets]) => [relation, new Set(targets.map(([t]) => t))])
    );

    const currentTraits = Array.from(world[$internal].entityTraits.get(entity)!);
    for (const trait of currentTraits) {
        if (trait === IsExcluded) continue;

        const relation = trait[$internal].relation;
        if (!relation) {
            if (!desiredTraits.has(trait)) removeTrait(world, entity, trait);
            continue;
        }

        const desiredTargets = desiredRelations.get(relation);
        if (!desiredTargets) {
            removeTrait(world, entity, trait);
            continue;
        }

        for (const target of getRelationTargets(world, relation, entity)) {
            if (!desiredTargets.has(target)) removeTrait(world, entity, relation(target));
        }
    }
}

function applyTraits(world: World, entity: Entity, traits: ResolvedSnapshot['traits']) {
    for (const [trait, value] of traits) {
        if (value === true || isTagTrait(trait)) {
            if (!hasTrait(world, entity, trait)) addTrait(world, entity, trait);
            continue;
        }

        const data = deepClone(value);
        if (!hasTrait(world, entity, trait)) {
            addTrait(world, entity, [trait, data]);
        } else if (!shallowEqual(getTrait(world, entity, trait), data)) {
            setTrait(world, entity, trait, data);
        }
    }
}

function isPrefix(prefix: readonly Entity[], list: readonly Entity[]) {
    if (prefix.length > list.length) return false;
    for (let i = 0; i < prefix.length; i++) {
        if (prefix[i] !== list[i]) return false;
    }
    return true;
}

function applyRelations(world: World, entity: Entity, relations: ResolvedRelationTargets) {
    for (const [relation, targets] of relations) {
        const relationTrait = relation[$internal].trait;
        const hasData = !isTagTrait(relationTrait);
        const isAoS = relationTrait[$internal].type === 'aos';

        // Rebuild non-exclusive relations whose target order has diverged so
        // that the resulting target order matches the snapshot.
        if (!relation[$internal].exclusive) {
            const current = getRelationTargets(world, relation, entity);
            const desired = targets.map(([target]) => target);
            if (current.length > 0 && !isPrefix(current, desired)) {
                removeTrait(world, entity, relation('*'));
            }
        }

        for (const [target, data] of targets) {
            const value = hasData && data !== undefined ? deepClone(data) : undefined;

            if (!hasRelationToTarget(world, relation, entity, target)) {
                addTrait(world, entity, relation(target, value as Record<string, unknown>));
                if (isAoS && value !== undefined) {
                    setTrait(world, entity, relation(target), value, false);
                }
            } else if (
                value !== undefined &&
                !shallowEqual(getRelationData(world, entity, relation, target), value)
            ) {
                setTrait(world, entity, relation(target), value);
            }
        }
    }
}

export function rollbackEntity(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
): void {
    assertEntityAlive(world, entity, 'roll back');
    assertEntitySnapshot(snapshot);

    const entityIndex = world[$internal].entityIndex;
    const { traits, relations } = resolveSnapshot(registry, snapshot);
    const resolvedRelations = resolveRelationTargets(relations, (id) =>
        getAliveEntityById(entityIndex, id)
    );

    removeUnlisted(world, entity, traits, resolvedRelations);
    applyTraits(world, entity, traits);
    applyRelations(world, entity, resolvedRelations);
}

export function rollbackWorld(
    world: World,
    registry: TraitRegistry,
    checkpoint: WorldSnapshot
): void {
    if (
        checkpoint === null ||
        typeof checkpoint !== 'object' ||
        !Array.isArray(checkpoint.entities)
    ) {
        throw new Error('Koota: Expected a world snapshot with an entities array.');
    }

    if (!world.isInitialized) world.init();

    const ctx = world[$internal];
    const worldEntityId = getEntityId(ctx.worldEntity);
    const ids = new Set<number>();

    const resolved = checkpoint.entities.map((snapshot) => {
        assertEntitySnapshot(snapshot);

        const id = snapshot.id;
        if (!isValidEntityId(id)) {
            throw new Error(`Koota: Invalid entity ID ${String(id)} in world snapshot.`);
        }
        if (id === worldEntityId) {
            throw new Error(
                `Koota: Entity ID ${id} in world snapshot is reserved for the world entity.`
            );
        }
        if (ids.has(id)) {
            throw new Error(`Koota: Duplicate entity ID ${id} in world snapshot.`);
        }
        ids.add(id);

        return { id, ...resolveSnapshot(registry, snapshot) };
    });

    // Validate every relation target before mutating the world.
    for (const { relations } of resolved) {
        resolveRelationTargets(relations, (id) => (ids.has(id) ? (id as Entity) : undefined));
    }

    for (const entity of world.entities) {
        if (entity !== ctx.worldEntity && world.has(entity)) destroyEntity(world, entity);
    }

    resolved.sort((a, b) => a.id - b.id);

    const entities = new Map<number, Entity>();
    for (const { id } of resolved) {
        entities.set(id, createEntityWithId(world, id));
    }

    for (const { id, traits } of resolved) {
        applyTraits(world, entities.get(id)!, traits);
    }

    for (const { id, relations } of resolved) {
        applyRelations(
            world,
            entities.get(id)!,
            resolveRelationTargets(relations, (targetId) => entities.get(targetId))
        );
    }
}

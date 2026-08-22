import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { getRelationTargets } from '../relation/relation';
import { isRelation } from '../relation/utils/is-relation';
import { hasTrait } from '../trait/trait';
import type { World } from '../world';
import type { EntitySnapshot, TraitRegistry, WorldSnapshot } from './types';
import {
    assertEntityAlive,
    assertEntityTraitsRegistered,
    snapshotRelationTargets,
    snapshotTraitValue,
} from './utils';

export function snapshotEntity(world: World, entity: Entity, registry: TraitRegistry): EntitySnapshot {
    assertEntityAlive(world, entity);
    assertEntityTraitsRegistered(world, entity, registry);

    const traits: EntitySnapshot['traits'] = {};
    const relations: NonNullable<EntitySnapshot['relations']> = {};

    for (const [key, entry] of registry.entries) {
        if (isRelation(entry)) {
            const targets = getRelationTargets(world, entry, entity);
            if (targets.length === 0) continue;
            relations[key] = snapshotRelationTargets(world, entity, entry);
            continue;
        }

        if (!hasTrait(world, entity, entry)) continue;
        traits[key] = snapshotTraitValue(world, entity, entry);
    }

    const snapshot: EntitySnapshot = {
        id: getEntityId(entity),
        traits,
    };

    if (Object.keys(relations).length > 0) {
        snapshot.relations = relations;
    }

    return snapshot;
}

export function snapshotWorld(world: World, registry: TraitRegistry): WorldSnapshot {
    const worldEntity = world[$internal].worldEntity;

    return {
        entities: world.entities
            .filter((entity) => entity !== worldEntity)
            .map((entity) => snapshotEntity(world, entity, registry)),
    };
}

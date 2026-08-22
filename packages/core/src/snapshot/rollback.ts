import { $internal } from '../common';
import { destroyEntity, restoreEntity } from '../entity/entity';
import type { Entity } from '../entity/types';
import { resetEntityIndexForRollback } from '../entity/utils/entity-index';
import { getEntityId } from '../entity/utils/pack-entity';
import { getRelationTargets } from '../relation/relation';
import { isRelation } from '../relation/utils/is-relation';
import { hasTrait } from '../trait/trait';
import type { ConfigurableTrait } from '../trait/types';
import type { World } from '../world';
import type { EntitySnapshot, TraitRegistry, WorldSnapshot } from './types';
import {
    assertEntityAlive,
    assertRegistryKey,
    collectSnapshotRegistryKeys,
    findEntityById,
} from './utils';

function validateSnapshotKeys(registry: TraitRegistry, snapshot: EntitySnapshot): void {
    for (const key of collectSnapshotRegistryKeys(snapshot)) {
        assertRegistryKey(registry, key);
    }
}

function removeRelationsNotInSnapshot(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
): void {
    for (const [key, entry] of registry.entries) {
        if (!isRelation(entry)) continue;

        const relationTrait = entry[$internal].trait;
        if (!hasTrait(world, entity, relationTrait)) continue;

        const snapshotTargets = snapshot.relations?.[key] ?? [];
        const snapshotTargetIds = new Set(snapshotTargets.map((target) => target.targetId));
        const currentTargets = getRelationTargets(world, entry, entity);

        for (const target of currentTargets) {
            if (!snapshotTargetIds.has(getEntityId(target))) {
                entity.remove(entry(target));
            }
        }
    }
}

function removeTraitsNotInSnapshot(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
): void {
    for (const [key, entry] of registry.entries) {
        if (isRelation(entry)) continue;
        if (hasTrait(world, entity, entry) && !(key in snapshot.traits)) {
            entity.remove(entry);
        }
    }
}

function applySnapshotTraits(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
): void {
    for (const [key, value] of Object.entries(snapshot.traits)) {
        const entry = registry.entries.get(key);
        if (!entry || isRelation(entry)) {
            throw new Error(`Koota: Unknown registry key "${key}".`);
        }

        if (value === true) {
            if (!hasTrait(world, entity, entry)) {
                entity.add(entry);
            }
            continue;
        }

        if (!hasTrait(world, entity, entry)) {
            entity.add([entry, value]);
        } else {
            entity.set(entry, value);
        }
    }
}

function applySnapshotRelations(
    world: World,
    entity: Entity,
    registry: TraitRegistry,
    snapshot: EntitySnapshot
): void {
    if (!snapshot.relations) return;

    for (const [key, targets] of Object.entries(snapshot.relations)) {
        const relation = registry.entries.get(key);
        if (!relation || !isRelation(relation)) {
            throw new Error(`Koota: Unknown registry key "${key}".`);
        }

        for (const { targetId, data } of targets) {
            const target = findEntityById(world, targetId);
            if (!target) {
                throw new Error(`Koota: Relation target entity ${targetId} does not exist.`);
            }

            const pair = relation(target);
            const configurable: ConfigurableTrait =
                data === undefined ? pair : (relation(target, data) as ConfigurableTrait);

            if (!entity.has(pair)) {
                entity.add(configurable);
            } else if (data !== undefined) {
                entity.set(pair, data);
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
    assertEntityAlive(world, entity);
    validateSnapshotKeys(registry, snapshot);

    removeRelationsNotInSnapshot(world, entity, registry, snapshot);
    removeTraitsNotInSnapshot(world, entity, registry, snapshot);
    applySnapshotTraits(world, entity, registry, snapshot);
    applySnapshotRelations(world, entity, registry, snapshot);
}

function validateWorldCheckpoint(registry: TraitRegistry, checkpoint: WorldSnapshot): void {
    if (!checkpoint || !Array.isArray(checkpoint.entities)) {
        throw new Error('Koota: Invalid world snapshot.');
    }

    const entityIds = new Set<number>();

    for (const snapshot of checkpoint.entities) {
        validateSnapshotKeys(registry, snapshot);
        entityIds.add(snapshot.id);
    }

    for (const snapshot of checkpoint.entities) {
        for (const targets of Object.values(snapshot.relations ?? {})) {
            for (const { targetId } of targets) {
                if (!entityIds.has(targetId)) {
                    throw new Error(`Koota: Dangling relation target entity ${targetId}.`);
                }
            }
        }
    }
}

export function rollbackWorld(
    world: World,
    registry: TraitRegistry,
    checkpoint: WorldSnapshot
): void {
    validateWorldCheckpoint(registry, checkpoint);

    const ctx = world[$internal];
    const worldEntity = ctx.worldEntity;

    for (const entity of [...world.entities]) {
        if (entity !== worldEntity) {
            destroyEntity(world, entity);
        }
    }

    resetEntityIndexForRollback(ctx.entityIndex, worldEntity);

    for (const snapshot of checkpoint.entities) {
        restoreEntity(world, snapshot.id);
    }

    for (const snapshot of checkpoint.entities) {
        const entity = findEntityById(world, snapshot.id);
        if (!entity) {
            throw new Error(`Koota: Failed to restore entity ${snapshot.id}.`);
        }
        rollbackEntity(world, entity, registry, snapshot);
    }
}

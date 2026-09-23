import type { Entity } from '../types';
import { getEntityId, incrementGeneration, packEntity } from './pack-entity';

export type EntityIndex = {
    /** The number of currently alive entities. */
    aliveCount: number;
    /** Array of packed entities, densely packed. */
    dense: Entity[];
    /** Sparse array mapping entity IDs to their index in the dense array. */
    sparse: number[];
    /** The highest entity ID that has been assigned. */
    maxId: number;
    /** The current world ID. */
    worldId: number;
};

/**
 * Creates and initializes a new EntityIndex.
 * @param worldId - The ID of the world this index belongs to.
 * @returns A new EntityIndex object.
 */
export const createEntityIndex = (worldId: number): EntityIndex => ({
    aliveCount: 0,
    dense: [],
    sparse: [],
    maxId: 0,
    worldId,
});

/**
 * Adds a new entity ID to the index or recycles an existing one.
 * @param index - The EntityIndex to add to.
 * @returns The new or recycled packed entity.
 */
export const allocateEntity = (index: EntityIndex): Entity => {
    if (index.aliveCount < index.dense.length) {
        // Recycle entity
        const recycledEntity = incrementGeneration(index.dense[index.aliveCount]);
        index.dense[index.aliveCount] = recycledEntity;
        index.sparse[getEntityId(recycledEntity)] = index.aliveCount;
        index.aliveCount++;

        return recycledEntity;
    }
    // Create new entity
    const id = index.maxId++;
    const entity = packEntity(index.worldId, 0, id);
    index.dense.push(entity);
    index.sparse[id] = index.aliveCount;
    index.aliveCount++;

    return entity;
};

/**
 * Removes an entity ID from the index.
 * @param index - The EntityIndex to remove from.
 * @param entity - The packed entity to remove.
 */
export const releaseEntity = (index: EntityIndex, entity: Entity): void => {
    const id = getEntityId(entity);
    const denseIndex = index.sparse[id];
    if (denseIndex === undefined || denseIndex >= index.aliveCount) return;

    const lastIndex = index.aliveCount - 1;
    const lastEntity = index.dense[lastIndex];
    const lastId = getEntityId(lastEntity);

    // Swap with the last element
    index.sparse[lastId] = denseIndex;
    index.dense[denseIndex] = lastEntity;
    // Update the removed entity's record
    index.sparse[id] = lastIndex;
    index.dense[lastIndex] = entity;
    index.aliveCount--;
};

/**
 * Reserves an entity without making it alive. A reserved entity is not part of the
 * index until it is activated with `activateReservedEntity` or returned to the
 * recycle pool with `releaseReservedEntity`.
 * @param index - The EntityIndex to reserve from.
 * @returns The reserved packed entity.
 */
export const reserveEntity = (index: EntityIndex): Entity => {
    if (index.aliveCount < index.dense.length) {
        // The sparse slot is left pointing past the dense array. Liveness checks compare
        // the stored entity, so the stale slot can never match.
        return incrementGeneration(index.dense.pop()!);
    }

    return packEntity(index.worldId, 0, index.maxId++);
};

/**
 * Makes a reserved entity alive.
 * @param index - The EntityIndex the entity was reserved from.
 * @param entity - The reserved packed entity.
 */
export const activateReservedEntity = (index: EntityIndex, entity: Entity): void => {
    const slot = index.aliveCount;
    const lastIndex = index.dense.length;

    // Move the first recyclable entity to the end to make room in the alive range.
    if (slot < lastIndex) {
        const recyclable = index.dense[slot];
        index.dense[lastIndex] = recyclable;
        index.sparse[getEntityId(recyclable)] = lastIndex;
    }

    index.dense[slot] = entity;
    index.sparse[getEntityId(entity)] = slot;
    index.aliveCount++;
};

/**
 * Returns a reserved entity to the recycle pool without it ever becoming alive.
 * @param index - The EntityIndex the entity was reserved from.
 * @param entity - The reserved packed entity.
 */
export const releaseReservedEntity = (index: EntityIndex, entity: Entity): void => {
    index.sparse[getEntityId(entity)] = index.dense.length;
    index.dense.push(entity);
};

/**
 * Checks if an entity ID is currently alive in the index.
 * @param index - The EntityIndex to check.
 * @param entity - The packed entity to check.
 * @returns True if the entity is alive, false otherwise.
 */
export const isEntityAlive = /* @inline @pure */ (index: EntityIndex, entity: Entity): boolean => {
    const denseIndex = index.sparse[getEntityId(entity)];
    if (denseIndex === undefined || denseIndex >= index.aliveCount) return false;
    return index.dense[denseIndex] === entity;
};

/**
 * Gets an array of all currently alive entities.
 * @param index - The EntityIndex to get alive entities from.
 * @returns An array of alive entities.
 */
export const getAliveEntities = (index: EntityIndex): Entity[] => {
    return index.dense.slice(0, index.aliveCount);
};

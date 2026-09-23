import type { Entity } from '../types';
import {
    getEntityGeneration,
    getEntityId,
    getEntityWorldId,
    incrementGeneration,
    packEntity,
} from './pack-entity';

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
 * Allocates a specific entity ID with a specific generation, e.g. to restore an entity from a snapshot.
 * @param index - The EntityIndex to add to.
 * @param id - The entity ID to allocate. Must not be alive.
 * @param generation - The generation to give the entity.
 * @returns The packed entity.
 */
export const allocateEntityAt = (index: EntityIndex, id: number, generation: number): Entity => {
    // Grow the index up to the ID, leaving the skipped IDs free for recycling.
    while (index.maxId <= id) {
        const freeId = index.maxId++;
        index.sparse[freeId] = index.dense.length;
        index.dense.push(packEntity(index.worldId, 0, freeId));
    }

    const denseIndex = index.sparse[id];
    if (denseIndex < index.aliveCount) throw new Error(`Koota: Entity ID ${id} is already in use.`);

    // Swap the ID into the first free slot of the dense array to make it alive.
    const aliveIndex = index.aliveCount;
    const displacedEntity = index.dense[aliveIndex];
    index.dense[denseIndex] = displacedEntity;
    index.sparse[getEntityId(displacedEntity)] = denseIndex;

    const entity = packEntity(index.worldId, generation, id);
    index.dense[aliveIndex] = entity;
    index.sparse[id] = aliveIndex;
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
 * Checks if an entity ID is currently alive in the index.
 * @param index - The EntityIndex to check.
 * @param entity - The packed entity to check.
 * @returns True if the entity is alive, false otherwise.
 */
export const isEntityAlive = /* @inline @pure */ (index: EntityIndex, entity: Entity): boolean => {
    const denseIndex = index.sparse[getEntityId(entity)];
    if (denseIndex === undefined || denseIndex >= index.aliveCount) return false;
    const storedEntity = index.dense[denseIndex];
    return (
        getEntityGeneration(entity) === getEntityGeneration(storedEntity) &&
        getEntityWorldId(entity) === index.worldId
    );
};

/**
 * Gets an array of all currently alive entities.
 * @param index - The EntityIndex to get alive entities from.
 * @returns An array of alive entities.
 */
export const getAliveEntities = (index: EntityIndex): Entity[] => {
    return index.dense.slice(0, index.aliveCount);
};

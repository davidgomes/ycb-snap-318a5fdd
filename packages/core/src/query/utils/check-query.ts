import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import type { World } from '../../world';
import type { QueryInstance } from '../types';
import { checkAspectConstraints } from './check-aspects';

/**
 * Check if an entity matches a non-tracking query.
 * For tracking queries, use checkQueryTracking instead.
 */
export function checkQuery(world: World, query: QueryInstance, entity: Entity): boolean {
    const staticBitmasks = query.staticBitmasks;
    const generations = query.generations;
    const ctx = world[$internal];
    const eid = getEntityId(entity);

    if (query.traitInstances.all.length === 0) return false;

    const hasAspectGroups = query.aspectNotGroups.length > 0 || query.aspectOrGroups.length > 0;
    // Or constraints spanning aspects are checked across all generations at once.
    const checkOrPerGeneration = query.aspectOrGroups.length === 0;

    for (let i = 0; i < generations.length; i++) {
        const generationId = generations[i];
        const bitmask = staticBitmasks[i];
        if (!bitmask) continue;

        const required = bitmask.required;
        const forbidden = bitmask.forbidden;
        const or = bitmask.or;
        const entityMask = ctx.entityMasks[generationId]?.[eid] || 0;

        if (!forbidden && !required && !or && !hasAspectGroups) return false;
        if (forbidden && (entityMask & forbidden) !== 0) return false;
        if (required && (entityMask & required) !== required) return false;
        if (checkOrPerGeneration && or !== 0 && (entityMask & or) === 0) return false;
    }

    if (hasAspectGroups) return checkAspectConstraints(query, ctx.entityMasks, eid);

    return true;
}

import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import type { World } from '../../world';
import type { QueryInstance } from '../types';
import { failsExclusionGroups, matchesDeferredOr } from './aspect-masks';

/**
 * Check if an entity matches a non-tracking query.
 * For tracking queries, use checkQueryTracking instead.
 */
export function checkQuery(world: World, query: QueryInstance, entity: Entity): boolean {
    const staticBitmasks = query.staticBitmasks;
    const generations = query.generations;
    const ctx = world[$internal];
    const eid = getEntityId(entity);
    const exclusionMasks = query.exclusionMasks;
    const orGroupMasks = query.orGroupMasks;
    const deferOr = orGroupMasks.length > 0;

    if (query.traitInstances.all.length === 0 && exclusionMasks.length === 0) return false;

    for (let i = 0; i < generations.length; i++) {
        const generationId = generations[i];
        const bitmask = staticBitmasks[i];
        if (!bitmask) continue;

        const required = bitmask.required;
        const forbidden = bitmask.forbidden;
        const or = bitmask.or;
        const entityMask = ctx.entityMasks[generationId]?.[eid] || 0;

        if (!forbidden && !required && !or) {
            if (!deferOr && exclusionMasks.length === 0) return false;
            continue;
        }
        if (forbidden && (entityMask & forbidden) !== 0) return false;
        if (required && (entityMask & required) !== required) return false;
        if (!deferOr && or !== 0 && (entityMask & or) === 0) return false;
    }

    if (exclusionMasks.length > 0 && failsExclusionGroups(exclusionMasks, ctx.entityMasks, eid)) {
        return false;
    }

    if (
        deferOr &&
        !matchesDeferredOr(orGroupMasks, generations, staticBitmasks, ctx.entityMasks, eid)
    ) {
        return false;
    }

    return true;
}

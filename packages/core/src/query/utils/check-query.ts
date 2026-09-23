import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import type { World } from '../../world';
import { passesPredicates, queryUsesPredicates } from '../predicate';
import type { QueryInstance } from '../types';

/**
 * Check if an entity matches a non-tracking query.
 * For tracking queries, use checkQueryTracking instead.
 */
export function checkQuery(world: World, query: QueryInstance, entity: Entity): boolean {
    const staticBitmasks = query.staticBitmasks;
    const generations = query.generations;
    const ctx = world[$internal];
    const eid = getEntityId(entity);
    const usesPredicates = queryUsesPredicates(query);

    if (query.traitInstances.all.length === 0 && !usesPredicates) return false;

    let hasOrTraits = false;
    let orFailed = false;

    for (let i = 0; i < generations.length; i++) {
        const generationId = generations[i];
        const bitmask = staticBitmasks[i];
        if (!bitmask) continue;

        const required = bitmask.required;
        const forbidden = bitmask.forbidden;
        const or = bitmask.or;
        const entityMask = ctx.entityMasks[generationId]?.[eid] || 0;

        if (!forbidden && !required && !or) {
            if (!usesPredicates) return false;
            continue;
        }
        if (forbidden && (entityMask & forbidden) !== 0) return false;
        if (required && (entityMask & required) !== required) return false;
        if (or !== 0) {
            hasOrTraits = true;
            if ((entityMask & or) === 0) {
                if (query.predicateFilters.or.length === 0) return false;
                orFailed = true;
            }
        }
    }

    if (usesPredicates && !passesPredicates(world, query, entity, orFailed, hasOrTraits, 'none')) {
        return false;
    }

    return true;
}

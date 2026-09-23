import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import type { World } from '../../world';
import { hasPredicateConstraints, passesPredicateConstraints } from '../predicate-match';
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

    if (query.traitInstances.all.length === 0) return false;

    const predicateFilters = query.predicateFilters;
    const hasOrPredicates = predicateFilters.or.length !== 0;
    let orTraitState: 'none' | 'pass' | 'fail' = 'none';

    for (let i = 0; i < generations.length; i++) {
        const generationId = generations[i];
        const bitmask = staticBitmasks[i];
        if (!bitmask) continue;

        const required = bitmask.required;
        const forbidden = bitmask.forbidden;
        const or = bitmask.or;
        const entityMask = ctx.entityMasks[generationId]?.[eid] || 0;

        if (!forbidden && !required && !or) return false;
        if (forbidden && (entityMask & forbidden) !== 0) return false;
        if (required && (entityMask & required) !== required) return false;
        if (or !== 0 && (entityMask & or) === 0) {
            if (!hasOrPredicates) return false;
            orTraitState = 'fail';
        } else if (hasOrPredicates && or !== 0 && orTraitState !== 'fail') {
            orTraitState = 'pass';
        }
    }

    if (!hasPredicateConstraints(query)) return true;
    return passesPredicateConstraints(world, query, eid, orTraitState);
}

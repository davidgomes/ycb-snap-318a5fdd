import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import type { World } from '../../world';
import type { QueryInstance } from '../types';
import { isPredicate } from '../predicate';
import { getStore } from '../../trait/trait';

/**
 * Check if an entity matches a non-tracking query.
 * For tracking queries, use checkQueryTracking instead.
 */
export function checkQuery(world: World, query: QueryInstance, entity: Entity): boolean {
    const staticBitmasks = query.staticBitmasks;
    const generations = query.generations;
    const ctx = world[$internal];
    const eid = getEntityId(entity);
    for (const predicate of query.predicates) {
        const values = predicate.traits.map((trait) => {
            if (!(world[$internal].entityTraits.get(entity)?.has(trait))) return undefined;
            return trait[$internal].get(eid, getStore(world, trait));
        });
        const current = isPredicate(predicate) && predicate.predicate(values);
        query.predicateState.set(eid, current);
        if (predicate.predicateMode !== 'current' || !current) return false;
    }

    if (query.traitInstances.all.length === 0) return false;

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
        if (or !== 0 && (entityMask & or) === 0) return false;
    }

    return true;
}

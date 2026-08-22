import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import type { World } from '../../world';
import type { QueryInstance } from '../types';
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

    if (query.traitInstances.all.length === 0) return false;

    for (let i = 0; i < generations.length; i++) {
        const generationId = generations[i];
        const bitmask = staticBitmasks[i];
        if (!bitmask) continue;

        const required = bitmask.required;
        const forbidden = bitmask.forbidden;
        const or = bitmask.or;
        const entityMask = ctx.entityMasks[generationId]?.[eid] || 0;

        if (!forbidden && !required && !or && query.predicates.length === 0 && query.predicateOrGroups.length === 0)
            return false;
        if (forbidden && (entityMask & forbidden) !== 0) return false;
        if (required && (entityMask & required) !== required) return false;
        if (or !== 0 && (entityMask & or) === 0) return false;
    }

    for (const predicate of query.predicates) {
        const data = predicate.dependencies.map((trait) => trait[$internal].get(eid, getStore(world, trait)));
        const value = Boolean(predicate.test(data));
        if (predicate.mode === 'not') {
            if (value) return false;
            continue;
        }
        if (predicate.mode === 'normal' || !predicate.mode) {
            if (!value) return false;
            continue;
        }
        const eidStates = query.predicateStates.get(predicate) ?? new Map<number, boolean>();
        const previous = eidStates.get(eid);
        eidStates.set(eid, value);
        query.predicateStates.set(predicate, eidStates);
        if (
            previous === undefined ||
            (predicate.mode === 'added' && !(previous === false && value === true)) ||
            (predicate.mode === 'removed' && !(previous === true && value === false)) ||
            (predicate.mode === 'changed' && previous === value)
        )
            return false;
    }
    for (const group of query.predicateOrGroups) {
        if (
            !group.some((predicate) => {
                const data = predicate.dependencies.map((trait) =>
                    trait[$internal].get(eid, getStore(world, trait))
                );
                const value = Boolean(predicate.test(data));
                return predicate.mode === 'not' ? !value : value;
            })
        )
            return false;
    }
    return true;
}

import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import type { World } from '../../world';
import type { PredicateTrackingGroup, QueryInstance } from '../types';

export function queryUsesPredicates(query: QueryInstance): boolean {
    return (
        query.requiredPredicates.length > 0 ||
        query.forbiddenPredicates.length > 0 ||
        query.orPredicates.length > 0 ||
        query.predicateTracking.length > 0
    );
}

/** Required and forbidden predicates. Or predicates are combined with other Or clauses. */
export function staticPredicateFiltersMatch(
    world: World,
    query: QueryInstance,
    entity: Entity
): boolean {
    const required = query.requiredPredicates;
    const forbidden = query.forbiddenPredicates;
    if (required.length === 0 && forbidden.length === 0) return true;

    const runtimes = world[$internal].predicateRuntimes;

    for (let i = 0; i < required.length; i++) {
        const runtime = runtimes.get(required[i].id);
        if (!runtime || !runtime.matches.has(entity)) return false;
    }

    for (let i = 0; i < forbidden.length; i++) {
        const runtime = runtimes.get(forbidden[i].id);
        if (runtime && runtime.matches.has(entity)) return false;
    }

    return true;
}

/** True when the entity has any static Or trait from the query. */
export function staticOrTraitsMatch(world: World, query: QueryInstance, entity: Entity): boolean {
    const orTraits = query.traitInstances.or;
    if (orTraits.length === 0) return false;

    const ctx = world[$internal];
    const eid = getEntityId(entity);
    for (let i = 0; i < orTraits.length; i++) {
        const instance = orTraits[i];
        const generation = ctx.entityMasks[instance.generationId];
        const mask = generation ? generation[eid] | 0 : 0;
        if ((mask & instance.bitflag) === instance.bitflag) return true;
    }
    return false;
}

/** True when any plain Or(predicate) currently matches. */
export function orPredicatesMatch(world: World, query: QueryInstance, entity: Entity): boolean {
    const orPreds = query.orPredicates;
    if (orPreds.length === 0) return false;

    const runtimes = world[$internal].predicateRuntimes;
    for (let i = 0; i < orPreds.length; i++) {
        const runtime = runtimes.get(orPreds[i].id);
        if (runtime && runtime.matches.has(entity)) return true;
    }
    return false;
}

function groupEventsMatch(group: PredicateTrackingGroup, entity: Entity): boolean {
    const events = group.events;
    const len = events.length;
    if (len === 0) return false;

    if (group.logic === 'or') {
        for (let i = 0; i < len; i++) {
            if (events[i].has(entity)) return true;
        }
        return false;
    }

    for (let i = 0; i < len; i++) {
        if (!events[i].has(entity)) return false;
    }
    return true;
}

/** Top-level Added/Removed/Changed(predicate) groups, which are AND. */
export function predicateAndTrackingOk(query: QueryInstance, entity: Entity): boolean {
    const groups = query.predicateTracking;
    for (let i = 0; i < groups.length; i++) {
        const group = groups[i];
        if (group.logic === 'or') continue;
        if (!groupEventsMatch(group, entity)) return false;
    }
    return true;
}

/** Or(Added(predicate), ...) groups. */
export function predicateOrTracking(
    query: QueryInstance,
    entity: Entity
): { hasOr: boolean; anyOr: boolean } {
    let hasOr = false;
    let anyOr = false;
    const groups = query.predicateTracking;
    for (let i = 0; i < groups.length; i++) {
        const group = groups[i];
        if (group.logic !== 'or') continue;
        hasOr = true;
        if (!anyOr && groupEventsMatch(group, entity)) anyOr = true;
    }
    return { hasOr, anyOr };
}

/**
 * Predicate filter for queries that do not use trait tracking groups.
 * Static Or traits, Or(predicate), and Or(Added/Removed/Changed(predicate)) are one Or.
 */
export function matchPredicateFilters(world: World, query: QueryInstance, entity: Entity): boolean {
    if (!staticPredicateFiltersMatch(world, query, entity)) return false;
    if (!predicateAndTrackingOk(query, entity)) return false;

    const orTracking = predicateOrTracking(query, entity);
    const hasPredicateOr = query.orPredicates.length > 0;
    const hasStaticOr = query.traitInstances.or.length > 0;
    if (!orTracking.hasOr && !hasPredicateOr && !hasStaticOr) return true;

    if (orTracking.anyOr) return true;
    if (hasPredicateOr && orPredicatesMatch(world, query, entity)) return true;
    if (hasStaticOr && staticOrTraitsMatch(world, query, entity)) return true;
    return false;
}

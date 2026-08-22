import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import type { World } from '../../world';
import type { QueryInstance } from '../types';
import { evaluateNotPredicate, evaluatePredicate } from './evaluate-predicate';

function checkRequiredPredicates(world: World, query: QueryInstance, entity: Entity): boolean {
    const predicates = query.predicates;

    for (let i = 0; i < predicates.required.length; i++) {
        if (!evaluatePredicate(world, entity, predicates.required[i])) return false;
    }

    for (let i = 0; i < predicates.not.length; i++) {
        if (!evaluateNotPredicate(world, entity, predicates.not[i])) return false;
    }

    return true;
}

function checkOrPredicates(world: World, query: QueryInstance, entity: Entity): boolean {
    const orPredicates = query.predicates.or;
    for (let i = 0; i < orPredicates.length; i++) {
        if (evaluatePredicate(world, entity, orPredicates[i])) return true;
    }

    const orNotPredicates = query.predicates.orNot;
    for (let i = 0; i < orNotPredicates.length; i++) {
        if (evaluateNotPredicate(world, entity, orNotPredicates[i])) return true;
    }

    return false;
}

/**
 * Check if an entity matches a non-tracking query.
 * For tracking queries, use checkQueryTracking instead.
 */
export function checkQuery(world: World, query: QueryInstance, entity: Entity): boolean {
    const staticBitmasks = query.staticBitmasks;
    const generations = query.generations;
    const ctx = world[$internal];
    const eid = getEntityId(entity);

    const hasTraitConstraints = query.traitInstances.all.some(
        (instance) =>
            query.traitInstances.required.includes(instance) ||
            query.traitInstances.forbidden.includes(instance) ||
            query.traitInstances.or.includes(instance)
    );
    const hasPredicates =
        query.predicates.required.length > 0 ||
        query.predicates.not.length > 0 ||
        query.predicates.or.length > 0;

    if (!hasTraitConstraints && !hasPredicates && query.predicates.tracking.length === 0) {
        return false;
    }

    let traitOrMatched = query.traitInstances.or.length === 0;

    for (let i = 0; i < generations.length; i++) {
        const generationId = generations[i];
        const bitmask = staticBitmasks[i];
        if (!bitmask) continue;

        const required = bitmask.required;
        const forbidden = bitmask.forbidden;
        const or = bitmask.or;
        const entityMask = ctx.entityMasks[generationId]?.[eid] || 0;

        if (forbidden && (entityMask & forbidden) !== 0) return false;
        if (required && (entityMask & required) !== required) return false;
        if (or !== 0 && (entityMask & or) !== 0) traitOrMatched = true;
    }

    const hasOrPredicates =
        query.predicates.or.length > 0 || query.predicates.orNot.length > 0;
    const hasOrTraits = query.traitInstances.or.length > 0;

    if (hasOrTraits || hasOrPredicates) {
        const orMatched = traitOrMatched || (hasOrPredicates && checkOrPredicates(world, query, entity));
        if (!orMatched) return false;
    }

    return checkRequiredPredicates(world, query, entity);
}

export function getPredicateMatchState(
    world: World,
    query: QueryInstance,
    entity: Entity,
    entry: QueryInstance['predicates']['tracking'][number]
): boolean {
    return evaluatePredicate(world, entity, entry.modifier);
}

export function getPreviousPredicateMatch(
    query: QueryInstance,
    trackingId: number,
    eid: number
): boolean {
    return query.predicateSnapshots.get(trackingId)?.[eid] ?? false;
}

export function setPreviousPredicateMatch(
    query: QueryInstance,
    trackingId: number,
    eid: number,
    matched: boolean
): void {
    let snapshot = query.predicateSnapshots.get(trackingId);
    if (!snapshot) {
        snapshot = [];
        query.predicateSnapshots.set(trackingId, snapshot);
    }
    snapshot[eid] = matched;
}

export function checkPredicateTracking(
    world: World,
    query: QueryInstance,
    entity: Entity,
    entry: QueryInstance['predicates']['tracking'][number]
): boolean {
    const eid = getEntityId(entity);
    const current = getPredicateMatchState(world, query, entity, entry);
    const previous = getPreviousPredicateMatch(query, entry.trackingId, eid);

    switch (entry.type) {
        case 'add':
            return current && !previous;
        case 'remove':
            return !current && previous;
        case 'change':
            return current !== previous;
    }
}

export function updatePredicateSnapshot(
    world: World,
    query: QueryInstance,
    entity: Entity,
    entry: QueryInstance['predicates']['tracking'][number]
): void {
    const eid = getEntityId(entity);
    const current = getPredicateMatchState(world, query, entity, entry);
    setPreviousPredicateMatch(query, entry.trackingId, eid, current);
}

export function checkAllPredicateTracking(world: World, query: QueryInstance, entity: Entity): boolean {
    const tracking = query.predicates.tracking;
    if (tracking.length === 0) return true;

    for (let i = 0; i < tracking.length; i++) {
        if (!checkPredicateTracking(world, query, entity, tracking[i])) return false;
    }

    return true;
}

import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import { hasRelationPair } from '../../relation/relation';
import type { World } from '../../world';
import type { QueryInstance } from '../types';
import {
    checkAllPredicateTracking,
    checkQuery,
    getPredicateMatchState,
    setPreviousPredicateMatch,
} from './check-query';
import { checkQueryTrackingWithRelations } from './check-query-tracking-with-relations';
import { checkQueryWithRelations } from './check-query-with-relations';

export function reevaluatePredicateQuery(world: World, query: QueryInstance, entity: Entity): void {
    const ctx = world[$internal];

    if (ctx.updateEachDepth > 0) {
        ctx.deferredPredicateEvaluations.add(query);
        ctx.deferredPredicateEntities.add(entity);
        return;
    }

    applyPredicateQueryUpdate(world, query, entity);
}

export function flushDeferredPredicateEvaluations(world: World): void {
    const ctx = world[$internal];
    if (ctx.deferredPredicateEvaluations.size === 0) return;

    const queries = [...ctx.deferredPredicateEvaluations];
    const entities = [...ctx.deferredPredicateEntities];

    ctx.deferredPredicateEvaluations.clear();
    ctx.deferredPredicateEntities.clear();

    for (const query of queries) {
        for (const entity of entities) {
            if (!world.has(entity)) continue;
            applyPredicateQueryUpdate(world, query, entity);
        }
    }
}

function passesStaticConstraints(world: World, query: QueryInstance, entity: Entity): boolean {
    if (query.relationFilters && query.relationFilters.length > 0) {
        for (const pair of query.relationFilters) {
            if (!hasRelationPair(world, entity, pair)) return false;
        }
    }

    const hasTraitFilters =
        query.traitInstances.required.length > 0 ||
        query.traitInstances.forbidden.length > 0 ||
        query.traitInstances.or.length > 0 ||
        query.predicates.required.length > 0 ||
        query.predicates.not.length > 0 ||
        query.predicates.or.length > 0 ||
        query.predicates.orNot.length > 0;

    if (!hasTraitFilters) return true;

    return query.relationFilters && query.relationFilters.length > 0
        ? checkQueryWithRelations(world, query, entity)
        : checkQuery(world, query, entity);
}

function applyPredicateQueryUpdate(world: World, query: QueryInstance, entity: Entity): void {
    query.toRemove.remove(entity);

    if (query.predicates.tracking.length > 0) {
        if (!passesStaticConstraints(world, query, entity)) {
            query.remove(world, entity);
            return;
        }

        if (checkAllPredicateTracking(world, query, entity)) {
            query.add(entity);
        } else {
            query.remove(world, entity);
        }

        return;
    }

    const match =
        query.relationFilters && query.relationFilters.length > 0
            ? checkQueryWithRelations(world, query, entity)
            : checkQuery(world, query, entity);

    if (match) query.add(entity);
    else query.remove(world, entity);
}

function updateTrackingSnapshots(world: World, query: QueryInstance, entity: Entity): void {
    for (const entry of query.predicates.tracking) {
        const eid = getEntityId(entity);
        const current = getPredicateMatchState(world, query, entity, entry);
        setPreviousPredicateMatch(query, entry.trackingId, eid, current);
    }
}

export function initializePredicateTrackingSnapshots(world: World, query: QueryInstance): void {
    const ctx = world[$internal];

    for (const entity of ctx.entityIndex.dense) {
        for (const entry of query.predicates.tracking) {
            const eid = getEntityId(entity);
            const current = getPredicateMatchState(world, query, entity, entry);
            setPreviousPredicateMatch(query, entry.trackingId, eid, current);
        }
    }
}

export function populatePredicateOnlyTrackingQuery(world: World, query: QueryInstance): void {
    const ctx = world[$internal];

    for (const entity of ctx.entityIndex.dense) {
        if (query.entities.has(entity)) continue;
        if (!passesStaticConstraints(world, query, entity)) continue;
        if (!checkAllPredicateTracking(world, query, entity)) continue;
        query.add(entity);
    }
}

export function updatePredicateSnapshotsAfterRun(world: World, query: QueryInstance): void {
    if (query.predicates.tracking.length === 0) return;

    const ctx = world[$internal];
    for (const entity of ctx.entityIndex.dense) {
        updateTrackingSnapshots(world, query, entity);
    }
}

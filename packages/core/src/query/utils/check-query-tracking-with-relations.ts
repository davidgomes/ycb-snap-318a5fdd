import type { Entity } from '../../entity/types';
import { hasRelationPair } from '../../relation/relation';
import type { World } from '../../world';
import type { EventType, QueryInstance } from '../types';
import { checkQueryTracking, hasTrackingOrGroup, trackingOrMatches } from './check-query-tracking';
import { evaluatePairFilters, setPairQueryRecheck } from './pair-tracking';

/**
 * Check if an entity matches a tracking query, including relation filters and pair-level events.
 */
export function matchTrackingQuery(
    world: World,
    query: QueryInstance,
    entity: Entity,
    eventType: EventType,
    eventGenerationId: number,
    eventBitflag: number
): boolean {
    const pairs =
        query.pairFilters && query.pairFilters.length > 0
            ? evaluatePairFilters(query, entity)
            : { andOk: true, orMatch: false, hasOr: false };

    if (
        !checkQueryTracking(
            world,
            query,
            entity,
            eventType,
            eventGenerationId,
            eventBitflag,
            pairs.hasOr
        )
    ) {
        return false;
    }

    if (query.relationFilters && query.relationFilters.length > 0) {
        for (const pair of query.relationFilters) {
            if (!hasRelationPair(world, entity, pair)) return false;
        }
    }

    if (!pairs.andOk) return false;

    if (pairs.hasOr || hasTrackingOrGroup(query)) {
        const traitOr = hasTrackingOrGroup(query) ? trackingOrMatches(query, entity) : false;
        if (pairs.hasOr) {
            if (!traitOr && !pairs.orMatch) return false;
        } else if (!traitOr) {
            return false;
        }
    }

    return true;
}

/**
 * @deprecated Use matchTrackingQuery. Kept so existing call sites keep relation checks.
 */
export function checkQueryTrackingWithRelations(
    world: World,
    query: QueryInstance,
    entity: Entity,
    eventType: EventType,
    eventGenerationId: number,
    eventBitflag: number
): boolean {
    return matchTrackingQuery(world, query, entity, eventType, eventGenerationId, eventBitflag);
}

setPairQueryRecheck((world, query, entity) => matchTrackingQuery(world, query, entity, 'add', 0, 0));

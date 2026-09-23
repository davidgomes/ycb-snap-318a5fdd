import type { Entity } from '../entity/types';
import { hasRelationPair } from '../relation/relation';
import { getTraitInstance } from '../trait/trait-instance';
import type { Trait } from '../trait/types';
import type { World } from '../world';
import { $internal } from '../common';
import type { QueryInstance } from './types';
import { updatePredicateLatches } from './predicate';
import { checkQuery } from './utils/check-query';
import { checkQueryTracking } from './utils/check-query-tracking';
import { checkQueryWithRelations } from './utils/check-query-with-relations';

export function schedulePredicateReevaluation(world: World, entity: Entity, trait: Trait) {
    const ctx = world[$internal];
    const instance = getTraitInstance(ctx.traitInstances, trait);
    if (!instance || instance.predicateQueries.size === 0) return;

    if (ctx.predicateDeferDepth > 0) {
        ctx.predicateDirty.push({ entity, trait });
        return;
    }

    reevaluatePredicateQueries(world, entity, trait);
}

export function schedulePredicateReevaluationForTraits(
    world: World,
    entity: Entity,
    traits: readonly Trait[]
) {
    for (let i = 0; i < traits.length; i++) {
        schedulePredicateReevaluation(world, entity, traits[i]);
    }
}

export function flushPredicateReevaluations(world: World) {
    const dirty = world[$internal].predicateDirty;
    if (dirty.length === 0) return;

    const seen = new Set<string>();
    for (let i = 0; i < dirty.length; i++) {
        const entry = dirty[i];
        const key = `${entry.entity}:${entry.trait.id}`;
        if (seen.has(key)) continue;
        seen.add(key);
        if (!world.has(entry.entity)) continue;
        reevaluatePredicateQueries(world, entry.entity, entry.trait);
    }
    dirty.length = 0;
}

export function reevaluatePredicateQueries(world: World, entity: Entity, trait: Trait) {
    const instance = getTraitInstance(world[$internal].traitInstances, trait);
    if (!instance || instance.predicateQueries.size === 0) return;

    for (const query of instance.predicateQueries) {
        if (query.predicateTrackingGroups.length > 0) {
            updatePredicateLatches(world, query, entity);
        }

        const match = entityMatchesQuery(world, query, entity);
        if (match) query.add(entity);
        else query.remove(world, entity);
    }
}

function entityMatchesQuery(world: World, query: QueryInstance, entity: Entity): boolean {
    let match: boolean;
    if (query.trackingGroups.length > 0) {
        match = checkQueryTracking(world, query, entity, 'change', 0, 0);
    } else if (query.relationFilters && query.relationFilters.length > 0) {
        match = checkQueryWithRelations(world, query, entity);
    } else {
        match = checkQuery(world, query, entity);
    }

    if (!match || !query.relationFilters || query.relationFilters.length === 0) return match;
    if (query.trackingGroups.length === 0) return match;

    for (let i = 0; i < query.relationFilters.length; i++) {
        if (!hasRelationPair(world, entity, query.relationFilters[i])) return false;
    }
    return true;
}

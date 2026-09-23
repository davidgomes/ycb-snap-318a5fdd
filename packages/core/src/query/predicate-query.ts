import { $internal } from '../common';
import type { Entity } from '../entity/types';
import type { Trait } from '../trait/types';
import { getTraitInstance } from '../trait/trait-instance';
import type { World } from '../world';
import type { Predicate, QueryInstance } from './types';
import { checkQuery } from './utils/check-query';
import { checkQueryTracking } from './utils/check-query-tracking';
import { checkQueryTrackingWithRelations } from './utils/check-query-tracking-with-relations';
import { checkQueryWithRelations } from './utils/check-query-with-relations';

export function watchPredicate(world: World, query: QueryInstance, predicate: Predicate): void {
    const ctx = world[$internal];
    const deps = predicate.dependencies;
    for (let i = 0; i < deps.length; i++) {
        const instance = getTraitInstance(ctx.traitInstances, deps[i]);
        if (instance) instance.predicateQueries.add(query);
    }
}

export function queryUsesPredicates(query: QueryInstance): boolean {
    return query.hasPredicateFilters || query.predicateTrackers.length > 0;
}

function entityStillAlive(world: World, entity: Entity): boolean {
    return world.has(entity);
}

export function recheckPredicateQuery(world: World, query: QueryInstance, entity: Entity): void {
    if (!entityStillAlive(world, entity)) return;

    const hasRelations = !!query.relationFilters && query.relationFilters.length > 0;
    const match = query.isTracking
        ? hasRelations
            ? checkQueryTrackingWithRelations(world, query, entity, 'change', 0, 0)
            : checkQueryTracking(world, query, entity, 'change', 0, 0)
        : hasRelations
          ? checkQueryWithRelations(world, query, entity)
          : checkQuery(world, query, entity);

    if (match) query.add(entity);
    else query.remove(world, entity);
}

export function queuePredicateRecheck(world: World, query: QueryInstance, entity: Entity): void {
    const ctx = world[$internal];
    ctx.deferredPredicateQueries.push(query);
    ctx.deferredPredicateEntities.push(entity);
}

/** Re-evaluate every predicate query watching this trait. */
export function recheckPredicatesForTrait(world: World, entity: Entity, trait: Trait): void {
    const instance = getTraitInstance(world[$internal].traitInstances, trait);
    if (!instance || instance.predicateQueries.size === 0) return;

    const ctx = world[$internal];
    if (ctx.deferPredicateDepth > 0) {
        for (const query of instance.predicateQueries) {
            queuePredicateRecheck(world, query, entity);
        }
        return;
    }

    for (const query of instance.predicateQueries) {
        recheckPredicateQuery(world, query, entity);
    }
}

export function beginPredicateDefer(world: World): void {
    world[$internal].deferPredicateDepth++;
}

export function endPredicateDefer(world: World): void {
    const ctx = world[$internal];
    ctx.deferPredicateDepth--;
    if (ctx.deferPredicateDepth > 0) return;

    const queries = ctx.deferredPredicateQueries;
    const entities = ctx.deferredPredicateEntities;
    const seen = new Set<string>();

    for (let i = 0; i < queries.length; i++) {
        const query = queries[i];
        const entity = entities[i];
        const key = query.hash + ':' + entity;
        if (seen.has(key)) continue;
        seen.add(key);
        recheckPredicateQuery(world, query, entity);
    }

    queries.length = 0;
    entities.length = 0;
}

import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import { getTraitInstance } from '../trait/trait-instance';
import type { Trait } from '../trait/types';
import type { World } from '../world/types';
import type { PredicateRuntime } from './predicate-state';
import { checkQueryTrackingWithRelations } from './utils/check-query-tracking-with-relations';
import { checkQueryWithRelations } from './utils/check-query-with-relations';
import { $predicate } from './symbols';
import type { Predicate, PredicateTuple, QueryInstance } from './types';

let nextPredicateId = 1;
let deferDepth = 0;
const deferred: { world: World; entity: Entity; traitId: number }[] = [];

/**
 * Create a value filter over dependency traits.
 * The predicate receives one array of dependency data, in dependency order.
 * Each call returns a distinct instance. Tags and relations are rejected.
 */
export function createPredicate<const T extends readonly Trait[]>(
    dependencies: T,
    test: (data: PredicateTuple<T>) => boolean
): Predicate<T>;
/** Relations and other non-traits are accepted so they can be rejected at runtime. */
export function createPredicate(
    dependencies: readonly unknown[],
    test: (data: any) => boolean
): Predicate;
export function createPredicate(dependencies: readonly unknown[], test: (data: any) => boolean): Predicate {
    if (!Array.isArray(dependencies)) {
        throw new Error('Koota: createPredicate expects an array of dependency traits.');
    }

    for (let i = 0; i < dependencies.length; i++) {
        const dependency = dependencies[i] as Trait | undefined;
        if (isRelation(dependency) || isRelationPair(dependency)) {
            throw new Error('Koota: Predicate dependencies cannot be relations.');
        }
        const internal = dependency?.[$internal];
        if (!dependency || !internal || typeof internal.get !== 'function') {
            throw new Error('Koota: Predicate dependencies must be traits.');
        }
        if (internal.relation) {
            throw new Error('Koota: Predicate dependencies cannot be relations.');
        }
        if (internal.type === 'tag') {
            throw new Error('Koota: Predicate dependencies cannot be tags.');
        }
    }

    const predicate: Predicate = {
        [$predicate]: true,
        id: nextPredicateId++,
        dependencies: dependencies as unknown as readonly Trait[],
        test: test as Predicate['test'],
    };

    return predicate;
}

export function ensurePredicateRuntime(world: World, predicate: Predicate): PredicateRuntime {
    const ctx = world[$internal];
    const existing = ctx.predicateRuntimes.get(predicate.id);
    if (existing) return existing;

    const runtime: PredicateRuntime = {
        predicate,
        truth: [],
        queries: new Set(),
    };
    ctx.predicateRuntimes.set(predicate.id, runtime);

    const dependencies = predicate.dependencies;
    for (let i = 0; i < dependencies.length; i++) {
        const traitId = dependencies[i].id;
        let list = ctx.predicatesByTrait.get(traitId);
        if (!list) {
            list = [];
            ctx.predicatesByTrait.set(traitId, list);
        }
        list.push(runtime);
    }

    const { dense, aliveCount } = ctx.entityIndex;
    for (let i = 0; i < aliveCount; i++) {
        const entity = dense[i];
        runtime.truth[getEntityId(entity)] = computePredicate(world, entity, predicate) ? 1 : 0;
    }

    return runtime;
}

/**
 * Record currently-true predicates as unseen Added matches.
 * The previous result starts empty, so the first read reports them.
 */
export function seedAddedPredicates(world: World, query: QueryInstance) {
    const groups = query.predicateTracking;
    if (groups.length === 0) return;

    const { dense, aliveCount } = world[$internal].entityIndex;
    for (let g = 0; g < groups.length; g++) {
        const group = groups[g];
        if (group.type !== 'add') continue;
        const predicates = group.predicates;
        for (let i = 0; i < aliveCount; i++) {
            const eid = getEntityId(dense[i]);
            let mask = 0;
            for (let p = 0; p < predicates.length; p++) {
                if (runtimeTruth(world, predicates[p], eid)) mask |= 1 << p;
            }
            if (mask) group.pending[eid] = mask;
        }
    }
}

/** Re-evaluate predicates that depend on `trait` after set, add, or remove. */
export function schedulePredicateReevaluation(world: World, entity: Entity, trait: Trait) {
    const list = world[$internal].predicatesByTrait.get(trait.id);
    if (!list || list.length === 0) return;

    if (deferDepth > 0) {
        deferred.push({ world, entity, traitId: trait.id });
        return;
    }

    reevaluateTrait(world, entity, trait.id);
}

export function beginPredicateDeferral() {
    deferDepth++;
}

export function endPredicateDeferral() {
    deferDepth--;
    if (deferDepth > 0) return;
    if (deferDepth < 0) deferDepth = 0;
    flushDeferredPredicates();
}

function flushDeferredPredicates() {
    if (deferred.length === 0) return;
    const batch = deferred.splice(0, deferred.length);
    const seen = new Set<string>();
    for (let i = 0; i < batch.length; i++) {
        const item = batch[i];
        const key = `${item.world.id}:${item.entity}:${item.traitId}`;
        if (seen.has(key)) continue;
        seen.add(key);
        reevaluateTrait(item.world, item.entity, item.traitId);
    }
}

function reevaluateTrait(world: World, entity: Entity, traitId: number) {
    const list = world[$internal].predicatesByTrait.get(traitId);
    if (!list) return;
    for (let i = 0; i < list.length; i++) {
        const runtime = list[i];
        applyPredicateTruth(world, entity, runtime, computePredicate(world, entity, runtime.predicate));
    }
}

function applyPredicateTruth(
    world: World,
    entity: Entity,
    runtime: PredicateRuntime,
    next: boolean
) {
    const eid = getEntityId(entity);
    const prev = runtime.truth[eid] === 1;
    if (prev === next) return;
    runtime.truth[eid] = next ? 1 : 0;

    const queries = Array.from(runtime.queries);
    for (let i = 0; i < queries.length; i++) {
        const query = queries[i];
        if (query.predicateTracking.length !== 0) {
            markPredicateTransition(query, runtime.predicate, eid, next);
        }
        refreshPredicateQuery(world, query, entity);
    }
}

function markPredicateTransition(query: QueryInstance, predicate: Predicate, eid: number, next: boolean) {
    const groups = query.predicateTracking;
    for (let g = 0; g < groups.length; g++) {
        const group = groups[g];
        const index = group.predicates.indexOf(predicate);
        if (index < 0) continue;
        const bit = 1 << index;
        let pending = group.pending[eid] | 0;
        if (group.type === 'change') {
            pending |= bit;
        } else if (group.type === 'add') {
            pending = next ? pending | bit : pending & ~bit;
        } else {
            pending = next ? pending & ~bit : pending | bit;
        }
        group.pending[eid] = pending;
    }
}

function refreshPredicateQuery(world: World, query: QueryInstance, entity: Entity) {
    const hasRelations = !!query.relationFilters && query.relationFilters.length > 0;
    let match: boolean;
    if (query.trackingGroups.length > 0) {
        match = hasRelations
            ? checkQueryTrackingWithRelations(world, query, entity, 'add', 0, 0)
            : query.checkTracking(world, entity, 'add', 0, 0);
    } else if (hasRelations) {
        match = checkQueryWithRelations(world, query, entity);
    } else {
        match = query.check(world, entity);
    }

    if (match) {
        if (!query.entities.has(entity) || query.toRemove.has(entity)) query.add(entity);
    } else {
        query.remove(world, entity);
    }
}

function computePredicate(world: World, entity: Entity, predicate: Predicate): boolean {
    const dependencies = predicate.dependencies;
    const data: unknown[] = [];
    for (let i = 0; i < dependencies.length; i++) {
        const value = readTraitData(world, entity, dependencies[i]);
        if (value === undefined) return false;
        data.push(value);
    }
    return !!predicate.test(data);
}

function readTraitData(world: World, entity: Entity, trait: Trait) {
    const ctx = world[$internal];
    const instance = getTraitInstance(ctx.traitInstances, trait);
    if (!instance) return undefined;

    const eid = getEntityId(entity);
    const masks = ctx.entityMasks[instance.generationId];
    if (!masks || (masks[eid] & instance.bitflag) !== instance.bitflag) return undefined;
    return trait[$internal].get(eid, instance.store);
}

function runtimeTruth(world: World, predicate: Predicate, eid: number): boolean {
    const runtime = world[$internal].predicateRuntimes.get(predicate.id);
    if (!runtime) return false;
    return runtime.truth[eid] === 1;
}

import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { getAliveEntities } from '../entity/utils/entity-index';
import { hasRelationPair } from '../relation/relation';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import { getStore, hasTrait, registerTrait } from '../trait/trait';
import { getTraitInstance, hasTraitInstance } from '../trait/trait-instance';
import type { Trait, TraitRecord } from '../trait/types';
import { SparseSet } from '../utils/sparse-set';
import type { World } from '../world';
import { getTrackingType } from './modifier';
import { $predicate } from './symbols';
import { staticPredicateFiltersMatch } from './utils/check-predicates';
import type { Modifier, Predicate, PredicateRuntime, PredicateTrackingGroup, QueryInstance } from './types';

let predicateId = 1;

type PredicateData<T extends readonly Trait[]> = {
    [K in keyof T]: T[K] extends Trait ? TraitRecord<T[K]> : never;
};

/**
 * Value filter over dependency traits.
 * The predicate receives one array of each dependency's data, in order.
 * Each call returns a distinct predicate.
 * Tag traits and relations are rejected.
 */
export function createPredicate<const T extends readonly Trait[]>(
    dependencies: T,
    fn: (data: PredicateData<T>) => boolean
): Predicate<T> {
    if (!Array.isArray(dependencies)) {
        throw new Error('Koota: createPredicate dependencies must be an array of traits.');
    }

    for (let i = 0; i < dependencies.length; i++) {
        assertPredicateDependency(dependencies[i]);
    }

    const id = predicateId++;
    return {
        [$predicate]: true,
        id,
        dependencies,
        fn: fn as (data: readonly unknown[]) => boolean,
    };
}

function assertPredicateDependency(dependency: unknown): asserts dependency is Trait {
    if (isRelation(dependency) || isRelationPair(dependency)) {
        throw new Error('Koota: createPredicate does not accept relations as dependencies.');
    }

    const trait = dependency as Trait;
    if (!trait || !trait[$internal]) {
        throw new Error('Koota: createPredicate dependencies must be traits.');
    }

    if (trait[$internal].relation) {
        throw new Error('Koota: createPredicate does not accept relations as dependencies.');
    }

    if (trait[$internal].type === 'tag') {
        throw new Error('Koota: createPredicate does not accept tag traits as dependencies.');
    }
}

export function isPredicate(value: unknown): value is Predicate {
    return (value as { [$predicate]?: boolean } | null | undefined)?.[$predicate] === true;
}

export function splitModifierInputs(
    inputs: readonly unknown[],
    mapRelations: boolean
): { traits: Trait[]; predicates: Predicate[] } {
    const traits: Trait[] = [];
    const predicates: Predicate[] = [];

    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];
        if (isPredicate(input)) {
            predicates.push(input);
        } else if (mapRelations && isRelation(input)) {
            traits.push(input[$internal].trait);
        } else {
            traits.push(input as Trait);
        }
    }

    return { traits, predicates };
}

export function linkRequiredPredicate(world: World, query: QueryInstance, predicate: Predicate) {
    query.requiredPredicates.push(predicate);
    linkPredicateRuntime(world, query, predicate);
}

export function linkForbiddenPredicate(world: World, query: QueryInstance, predicate: Predicate) {
    query.forbiddenPredicates.push(predicate);
    linkPredicateRuntime(world, query, predicate);
}

export function linkOrPredicate(world: World, query: QueryInstance, predicate: Predicate) {
    query.orPredicates.push(predicate);
    linkPredicateRuntime(world, query, predicate);
}

export function registerPredicateTracking(
    world: World,
    query: QueryInstance,
    modifier: Modifier,
    logic: 'and' | 'or',
    groups: Map<string, PredicateTrackingGroup>
) {
    const predicates = modifier.predicates;
    if (!predicates || predicates.length === 0) return;

    const trackingType = getTrackingType(modifier);
    if (!trackingType) return;

    const key = `p-${trackingType}-${modifier.id}-${logic}`;
    let group = groups.get(key);
    if (!group) {
        group = {
            logic,
            type: trackingType,
            id: modifier.id,
            predicates: [],
            committed: [],
            events: [],
        };
        groups.set(key, group);
        query.predicateTracking.push(group);
    }

    for (let i = 0; i < predicates.length; i++) {
        const predicate = predicates[i];
        if (group.predicates.includes(predicate)) continue;
        group.predicates.push(predicate);
        group.committed.push(new SparseSet());
        group.events.push(new SparseSet());
        linkPredicateRuntime(world, query, predicate);
    }

    query.isTracking = true;
}

/** Seed Added(predicate) so entities already matching are not in the previous result. */
export function seedPredicateTracking(world: World, query: QueryInstance) {
    const ctx = world[$internal];
    const groups = query.predicateTracking;
    for (let g = 0; g < groups.length; g++) {
        const group = groups[g];
        if (group.type !== 'add') continue;
        for (let i = 0; i < group.predicates.length; i++) {
            const runtime = ctx.predicateRuntimes.get(group.predicates[i].id);
            if (!runtime) continue;
            const dense = runtime.matches.dense;
            const events = group.events[i];
            for (let j = 0; j < dense.length; j++) events.add(dense[j]);
        }
    }
}

/**
 * Commit predicate tracking for entities this query just returned.
 * Unobserved Added / Removed / Changed events stay pending so a filter
 * that excluded an entity does not consume its transition.
 */
export function commitPredicateTracking(
    world: World,
    query: QueryInstance,
    returned: readonly Entity[]
) {
    if (query.predicateTracking.length === 0) return;

    const ctx = world[$internal];
    const returnedSet = new Set<number>(returned);
    const groups = query.predicateTracking;

    for (let g = 0; g < groups.length; g++) {
        const group = groups[g];
        for (let i = 0; i < group.predicates.length; i++) {
            const runtime = ctx.predicateRuntimes.get(group.predicates[i].id);
            const committed = group.committed[i];
            const events = group.events[i];

            if (group.type === 'add') {
                const previously = committed.dense.slice();
                for (let k = 0; k < previously.length; k++) {
                    const entity = previously[k] as Entity;
                    if (returnedSet.has(entity)) continue;

                    const stillTrue = !!runtime?.matches.has(entity);
                    committed.remove(entity);

                    if (stillTrue && passesStructuralFilters(world, query, entity)) {
                        // This read drained a full match. Leave no event so the
                        // same truth does not repeat, but the next false->true does.
                        continue;
                    }

                    if (stillTrue) events.add(entity);
                    else events.remove(entity);
                }
            }

            for (let k = 0; k < returned.length; k++) {
                const entity = returned[k];
                events.remove(entity);
                if (group.type !== 'add') continue;
                if (runtime?.matches.has(entity)) committed.add(entity);
                else committed.remove(entity);
            }
        }
    }
}

/**
 * Re-evaluate every predicate that depends on `trait` for `entity`.
 * While an updateEach iteration is active, the work is queued until it ends.
 */
export function observeTraitDependencies(world: World, entity: Entity, trait: Trait) {
    const ctx = world[$internal];
    const instance = getTraitInstance(ctx.traitInstances, trait);
    if (!instance || instance.dependentPredicates.size === 0) return;

    if (ctx.predicateDeferDepth > 0) {
        for (const predicate of instance.dependentPredicates) {
            const key = `${entity}:${predicate.id}`;
            if (ctx.deferredPredicateKeys.has(key)) continue;
            ctx.deferredPredicateKeys.add(key);
            ctx.deferredPredicateWork.push({ entityId: entity, predicateId: predicate.id });
        }
        return;
    }

    for (const predicate of instance.dependentPredicates) {
        reevaluatePredicate(world, entity, predicate, true);
    }
}

/** Queue predicate re-evaluation for traits written by updateEach. */
export function observeIteratedDependencies(world: World, entity: Entity, traits: readonly Trait[]) {
    const instances = world[$internal].traitInstances;
    for (let i = 0; i < traits.length; i++) {
        const instance = getTraitInstance(instances, traits[i]);
        if (!instance || instance.dependentPredicates.size === 0) continue;
        observeTraitDependencies(world, entity, traits[i]);
    }
}

export function flushDeferredPredicates(world: World) {
    const ctx = world[$internal];
    if (ctx.predicateDeferDepth > 0) return;
    if (ctx.deferredPredicateWork.length === 0) return;

    const work = ctx.deferredPredicateWork.splice(0, ctx.deferredPredicateWork.length);
    ctx.deferredPredicateKeys.clear();

    for (let i = 0; i < work.length; i++) {
        const item = work[i];
        const runtime = ctx.predicateRuntimes.get(item.predicateId);
        if (!runtime) continue;
        reevaluatePredicate(world, item.entityId as Entity, runtime.predicate, true);
    }

    if (ctx.deferredPredicateWork.length > 0) flushDeferredPredicates(world);
}

/** Required traits, forbidden traits, and relation targets. Tracking state is ignored. */
function passesStructuralFilters(world: World, query: QueryInstance, entity: Entity): boolean {
    const ctx = world[$internal];
    const eid = getEntityId(entity);
    const generations = query.generations;
    const bitmasks = query.staticBitmasks;

    for (let i = 0; i < generations.length; i++) {
        const bitmask = bitmasks[i];
        if (!bitmask) continue;
        const generation = ctx.entityMasks[generations[i]];
        const entityMask = generation ? generation[eid] | 0 : 0;
        if (bitmask.forbidden && (entityMask & bitmask.forbidden) !== 0) return false;
        if (bitmask.required && (entityMask & bitmask.required) !== bitmask.required) return false;
    }

    if (!staticPredicateFiltersMatch(world, query, entity)) return false;

    const filters = query.relationFilters;
    if (filters && filters.length > 0) {
        for (let i = 0; i < filters.length; i++) {
            if (!hasRelationPair(world, entity, filters[i])) return false;
        }
    }

    return true;
}

function linkPredicateRuntime(world: World, query: QueryInstance, predicate: Predicate) {
    ensurePredicateRuntime(world, predicate).queries.add(query);
}

function ensurePredicateRuntime(world: World, predicate: Predicate): PredicateRuntime {
    const ctx = world[$internal];
    const existing = ctx.predicateRuntimes.get(predicate.id);
    if (existing) return existing;

    const runtime: PredicateRuntime = {
        predicate,
        matches: new SparseSet(),
        queries: new Set(),
    };
    ctx.predicateRuntimes.set(predicate.id, runtime);

    const dependencies = predicate.dependencies;
    for (let i = 0; i < dependencies.length; i++) {
        const dependency = dependencies[i];
        if (!hasTraitInstance(ctx.traitInstances, dependency)) registerTrait(world, dependency);
        getTraitInstance(ctx.traitInstances, dependency)!.dependentPredicates.add(predicate);
    }

    const alive = getAliveEntities(ctx.entityIndex);
    for (let i = 0; i < alive.length; i++) {
        if (computePredicate(world, alive[i], predicate)) runtime.matches.add(alive[i]);
    }

    return runtime;
}

function computePredicate(world: World, entity: Entity, predicate: Predicate): boolean {
    const dependencies = predicate.dependencies;
    const data: unknown[] = Array.from({ length: dependencies.length });

    for (let i = 0; i < dependencies.length; i++) {
        const dependency = dependencies[i];
        if (!hasTrait(world, entity, dependency)) return false;
        const store = getStore(world, dependency);
        data[i] = dependency[$internal].get(getEntityId(entity), store);
    }

    return !!predicate.fn(data);
}

function reevaluatePredicate(
    world: World,
    entity: Entity,
    predicate: Predicate,
    syncMembership: boolean
) {
    const runtime = world[$internal].predicateRuntimes.get(predicate.id);
    if (!runtime) return;

    const was = runtime.matches.has(entity);
    const now = computePredicate(world, entity, predicate);
    if (was === now) return;

    if (now) runtime.matches.add(entity);
    else runtime.matches.remove(entity);

    const queries = Array.from(runtime.queries);
    for (let i = 0; i < queries.length; i++) {
        const query = queries[i];
        recordPredicateTransition(query, entity, predicate, now);
        if (syncMembership) syncQueryMembership(world, query, entity);
    }
}

function recordPredicateTransition(
    query: QueryInstance,
    entity: Entity,
    predicate: Predicate,
    now: boolean
) {
    const groups = query.predicateTracking;
    for (let g = 0; g < groups.length; g++) {
        const group = groups[g];
        const predicates = group.predicates;
        for (let i = 0; i < predicates.length; i++) {
            if (predicates[i] !== predicate) continue;
            const events = group.events[i];
            if (group.type === 'add') {
                if (now && !group.committed[i].has(entity)) events.add(entity);
                else events.remove(entity);
            } else if (group.type === 'remove') {
                if (!now) events.add(entity);
                else events.remove(entity);
            } else {
                events.add(entity);
            }
        }
    }
}

function syncQueryMembership(world: World, query: QueryInstance, entity: Entity) {
    let match = query.isTracking
        ? query.checkTracking(world, entity, 'change', 0, 0)
        : query.check(world, entity);

    if (match && query.relationFilters && query.relationFilters.length > 0) {
        for (let i = 0; i < query.relationFilters.length; i++) {
            if (!hasRelationPair(world, entity, query.relationFilters[i])) {
                match = false;
                break;
            }
        }
    }

    if (match) query.add(entity);
    else query.remove(world, entity);
}

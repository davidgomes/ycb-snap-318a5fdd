import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { isRelation } from '../relation/utils/is-relation';
import { getTraitInstance } from '../trait/trait-instance';
import type { Trait, TraitRecord } from '../trait/types';
import type { World } from '../world';
import { $predicate } from './symbols';
import type { Predicate, PredicateData, QueryInstance } from './types';

let predicateId = 0;

export function isPredicate(value: unknown): value is Predicate {
    return (value as Predicate | null | undefined)?.[$predicate] === true;
}

/**
 * Create a value filter. The predicate receives dependency trait data in order.
 * Tags and relations are rejected. Each call returns a distinct instance.
 */
export function createPredicate<TDeps extends Trait[]>(
    dependencies: [...TDeps],
    test: (data: PredicateData<TDeps>) => boolean
): Predicate<TDeps> {
    if (dependencies.length === 0) {
        throw new Error('Koota: createPredicate requires at least one dependency trait.');
    }

    for (let i = 0; i < dependencies.length; i++) {
        const dep = dependencies[i] as Trait | unknown;
        if (
            isRelation(dep) ||
            (dep as Trait)?.[$internal]?.type === 'tag' ||
            (dep as Trait)?.[$internal]?.relation
        ) {
            throw new Error('Koota: createPredicate dependencies cannot be tags or relations.');
        }
    }

    return {
        [$predicate]: true,
        id: predicateId++,
        dependencies,
        test: test as (data: PredicateData<TDeps>) => boolean,
    };
}

/** True when every dependency is present and the predicate function returns true. */
export function evalPredicate(world: World, entity: Entity, predicate: Predicate): boolean {
    const ctx = world[$internal];
    const deps = predicate.dependencies;
    const data = new Array(deps.length);
    const eid = getEntityId(entity);

    for (let i = 0; i < deps.length; i++) {
        const dep = deps[i];
        const instance = getTraitInstance(ctx.traitInstances, dep);
        if (!instance) return false;

        const masks = ctx.entityMasks[instance.generationId];
        const mask = masks ? masks[eid] | 0 : 0;
        if ((mask & instance.bitflag) === 0) return false;

        data[i] = dep[$internal].get(eid, instance.store) as TraitRecord<Trait>;
    }

    return !!predicate.test(data);
}

/**
 * Static predicate filters (require / not / or).
 * `orTraitFailed` is set when an Or() trait bitmask missed and predicates may still save the match.
 */
export function matchPredicateFilters(
    world: World,
    query: QueryInstance,
    entity: Entity,
    orTraitFailed: boolean
): boolean {
    const filters = query.predicateFilters;
    const required = filters.require;
    for (let i = 0; i < required.length; i++) {
        if (!evalPredicate(world, entity, required[i])) return false;
    }

    const forbidden = filters.not;
    for (let i = 0; i < forbidden.length; i++) {
        if (evalPredicate(world, entity, forbidden[i])) return false;
    }

    const ors = filters.or;
    if (ors.length > 0 && (orTraitFailed || query.traitInstances.or.length === 0)) {
        let any = false;
        for (let i = 0; i < ors.length; i++) {
            if (evalPredicate(world, entity, ors[i])) {
                any = true;
                break;
            }
        }
        if (!any) return false;
    }

    return true;
}

/** Added / Removed / Changed truthiness against the last committed query result. */
export function matchPredicateTrackers(world: World, query: QueryInstance, entity: Entity): boolean {
    const trackers = query.predicateTrackers;
    const eid = getEntityId(entity);
    let hasOr = false;
    let anyOr = false;

    for (let i = 0; i < trackers.length; i++) {
        const tracker = trackers[i];
        const current = evalPredicate(world, entity, tracker.predicate);
        const prev = tracker.committed[eid] === 1;
        let ok = false;

        if (tracker.kind === 'add') ok = current && !prev;
        else if (tracker.kind === 'remove') ok = !current && prev;
        else ok = current !== prev;

        if (tracker.logic === 'or') {
            hasOr = true;
            if (ok) anyOr = true;
        } else if (!ok) {
            return false;
        }
    }

    if (hasOr && !anyOr) return false;
    return true;
}

/** Snapshot current predicate truth so the next query observes transitions from this result. */
export function commitPredicateTrackers(world: World, query: QueryInstance): void {
    const trackers = query.predicateTrackers;
    if (trackers.length === 0) return;

    const dense = world[$internal].entityIndex.dense;
    for (let i = 0; i < dense.length; i++) {
        const entity = dense[i];
        const eid = getEntityId(entity);
        for (let j = 0; j < trackers.length; j++) {
            const tracker = trackers[j];
            if (eid >= tracker.committed.length) {
                const next = new Uint8Array(Math.max(eid + 1, tracker.committed.length * 2));
                next.set(tracker.committed);
                tracker.committed = next;
            }
            tracker.committed[eid] = evalPredicate(world, entity, tracker.predicate) ? 1 : 0;
        }
    }
}

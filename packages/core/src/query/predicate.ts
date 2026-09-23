import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import { getTraitInstance } from '../trait/trait-instance';
import type { Trait, TraitRecord } from '../trait/types';
import type { World } from '../world';
import type { QueryInstance } from './types';

export const $predicate = Symbol('predicate');

export type PredicateData<T extends readonly Trait[]> = {
    [K in keyof T]: T[K] extends Trait ? TraitRecord<T[K]> : never;
};

export type Predicate<T extends readonly Trait[] = readonly Trait[]> = {
    readonly [$predicate]: true;
    readonly id: number;
    readonly dependencies: T;
    fn(data: PredicateData<T>): boolean;
};

export type PredicateTrackingGroup = {
    logic: 'and' | 'or';
    type: 'add' | 'remove' | 'change';
    predicates: Predicate[];
    committed: (boolean | undefined)[][];
    current: (boolean | undefined)[][];
    latched: (boolean | undefined)[];
};

export type TraitOrState = 'none' | 'matched' | 'failed';

let predicateId = 1;

export function isPredicate(value: unknown): value is Predicate {
    return (value as { [$predicate]?: true } | null | undefined)?.[$predicate] === true;
}

export function createPredicate<const T extends readonly Trait[]>(
    dependencies: T,
    fn: (data: PredicateData<T>) => boolean
): Predicate<T> {
    for (let i = 0; i < dependencies.length; i++) {
        const dependency: unknown = dependencies[i];
        if (
            isRelation(dependency) ||
            isRelationPair(dependency) ||
            (dependency as Trait)?.[$internal]?.relation
        ) {
            throw new Error('Koota: Predicates cannot depend on relations.');
        }
        if ((dependency as Trait)?.[$internal]?.type === 'tag') {
            throw new Error('Koota: Predicates cannot depend on tag traits.');
        }
    }

    return {
        [$predicate]: true,
        id: predicateId++,
        dependencies,
        fn,
    };
}

export function queryUsesPredicates(query: QueryInstance): boolean {
    const filters = query.predicateFilters;
    return (
        filters.required.length > 0 ||
        filters.forbidden.length > 0 ||
        filters.or.length > 0 ||
        query.predicateTrackingGroups.length > 0
    );
}

/** True when every dependency is present and the predicate function is truthy. */
export function evaluatePredicate(world: World, entity: Entity, predicate: Predicate): boolean {
    const dependencies = predicate.dependencies;
    const ctx = world[$internal];
    const eid = getEntityId(entity);
    const data = Array.from({ length: dependencies.length });

    for (let i = 0; i < dependencies.length; i++) {
        const dependency = dependencies[i];
        const instance = getTraitInstance(ctx.traitInstances, dependency);
        if (!instance) return false;

        const masks = ctx.entityMasks[instance.generationId];
        const mask = masks ? masks[eid] | 0 : 0;
        if ((mask & instance.bitflag) !== instance.bitflag) return false;

        data[i] = dependency[$internal].get(eid, instance.store);
    }

    return !!predicate.fn(data);
}

export function updatePredicateLatches(world: World, query: QueryInstance, entity: Entity) {
    const eid = getEntityId(entity);
    const groups = query.predicateTrackingGroups;

    for (let g = 0; g < groups.length; g++) {
        const group = groups[g];
        let groupMatch = group.logic === 'and';

        for (let p = 0; p < group.predicates.length; p++) {
            const now = evaluatePredicate(world, entity, group.predicates[p]);
            const current = group.current[p];
            const committed = group.committed[p];
            if (eid >= current.length) {
                current.length = eid + 1;
                committed.length = eid + 1;
            }
            current[eid] = now;

            const was = committed[eid] === true;
            let predMatch = false;
            if (group.type === 'add') predMatch = now && !was;
            else if (group.type === 'remove') predMatch = !now && was;
            else predMatch = now !== was;

            if (group.logic === 'and') {
                if (!predMatch) groupMatch = false;
            } else if (predMatch) {
                groupMatch = true;
            }
        }

        if (eid >= group.latched.length) group.latched.length = eid + 1;
        group.latched[eid] = groupMatch;
    }
}

export function commitPredicateTracking(query: QueryInstance) {
    const groups = query.predicateTrackingGroups;
    for (let g = 0; g < groups.length; g++) {
        const group = groups[g];
        for (let p = 0; p < group.predicates.length; p++) {
            const current = group.current[p];
            const committed = group.committed[p];
            for (let eid = 0; eid < current.length; eid++) {
                if (current[eid] !== undefined) committed[eid] = current[eid];
            }
        }
        group.latched = [];
    }
}

export function passesPredicates(
    world: World,
    query: QueryInstance,
    entity: Entity,
    orFailed: boolean,
    hasOrTraits: boolean,
    traitOrState: TraitOrState
): boolean {
    const filters = query.predicateFilters;
    const eid = getEntityId(entity);

    for (let i = 0; i < filters.required.length; i++) {
        if (!evaluatePredicate(world, entity, filters.required[i])) return false;
    }
    for (let i = 0; i < filters.forbidden.length; i++) {
        if (evaluatePredicate(world, entity, filters.forbidden[i])) return false;
    }

    let predicateOr = false;
    let predicateOrMatched = false;
    const groups = query.predicateTrackingGroups;
    for (let i = 0; i < groups.length; i++) {
        const group = groups[i];
        const latched = group.latched[eid] === true;
        if (group.logic === 'or') {
            predicateOr = true;
            if (latched) predicateOrMatched = true;
        } else if (!latched) {
            return false;
        }
    }

    if (hasOrTraits || filters.or.length > 0) {
        if (!(hasOrTraits && !orFailed)) {
            let matched = false;
            for (let i = 0; i < filters.or.length; i++) {
                if (evaluatePredicate(world, entity, filters.or[i])) {
                    matched = true;
                    break;
                }
            }
            if (!matched) return false;
        }
    }

    if (traitOrState === 'matched') return true;
    if (traitOrState === 'failed') return predicateOrMatched;
    if (predicateOr && !predicateOrMatched) return false;
    return true;
}

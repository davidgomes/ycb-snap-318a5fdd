import type { World } from '../world/types';
import { isPredicateTrue } from './predicate-state';
import type { PredicateTrackingGroup, QueryInstance } from './types';

export type OrTraitState = 'none' | 'pass' | 'fail';

export function hasPredicateConstraints(query: QueryInstance): boolean {
    const filters = query.predicateFilters;
    return (
        filters.required.length !== 0 ||
        filters.not.length !== 0 ||
        filters.or.length !== 0 ||
        query.predicateTracking.length !== 0
    );
}

/**
 * Static predicate filters plus Added/Removed/Changed predicate tracking.
 * `orTraits` is the structural Or() trait result, so a true predicate can
 * satisfy Or(trait, predicate) when the trait bits miss.
 */
export function passesPredicateConstraints(
    world: World,
    query: QueryInstance,
    eid: number,
    orTraits: OrTraitState
): boolean {
    const filters = query.predicateFilters;

    if (orTraits !== 'pass' && (orTraits === 'fail' || filters.or.length !== 0)) {
        let predicateOr = false;
        const orPredicates = filters.or;
        for (let i = 0; i < orPredicates.length; i++) {
            if (isPredicateTrue(world, orPredicates[i], eid)) {
                predicateOr = true;
                break;
            }
        }
        if (!predicateOr) return false;
    }

    const required = filters.required;
    for (let i = 0; i < required.length; i++) {
        if (!isPredicateTrue(world, required[i], eid)) return false;
    }

    const forbidden = filters.not;
    for (let i = 0; i < forbidden.length; i++) {
        if (isPredicateTrue(world, forbidden[i], eid)) return false;
    }

    return predicateTrackingPasses(world, query, eid);
}

function predicateTrackingPasses(world: World, query: QueryInstance, eid: number): boolean {
    const groups = query.predicateTracking;
    const len = groups.length;
    if (len === 0) return true;

    let hasOrGroup = false;
    let anyOrMatched = false;

    for (let i = 0; i < len; i++) {
        const group = groups[i];
        const matched = predicateGroupMatches(world, group, eid);
        if (group.logic === 'or') {
            hasOrGroup = true;
            if (matched) anyOrMatched = true;
        } else if (!matched) {
            return false;
        }
    }

    if (hasOrGroup && !anyOrMatched) return false;
    return true;
}

function predicateGroupMatches(world: World, group: PredicateTrackingGroup, eid: number): boolean {
    const predicates = group.predicates;
    const pending = group.pending[eid] | 0;
    const len = predicates.length;

    if (group.logic === 'or') {
        for (let i = 0; i < len; i++) {
            if (predicateEventMatches(world, group, predicates[i], eid, pending, i)) return true;
        }
        return false;
    }

    for (let i = 0; i < len; i++) {
        if (!predicateEventMatches(world, group, predicates[i], eid, pending, i)) return false;
    }
    return true;
}

function predicateEventMatches(
    world: World,
    group: PredicateTrackingGroup,
    predicate: PredicateTrackingGroup['predicates'][number],
    eid: number,
    pending: number,
    index: number
): boolean {
    const hasPending = (pending & (1 << index)) !== 0;
    if (group.type === 'change') return hasPending;

    const isTrue = isPredicateTrue(world, predicate, eid);
    if (group.type === 'add') return hasPending && isTrue;
    return hasPending && !isTrue;
}

import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import { getTraitInstance } from '../../trait/trait-instance';
import type { Trait } from '../../trait/types';
import type { World } from '../../world';
import type { EventType, PairGroup, PairState, QueryInstance } from '../types';
import { checkQueryTrackingWithRelations } from './check-query-tracking-with-relations';
import { getTrackingCursor } from './tracking-cursor';

const FIRST_TRACKING_ID = 3;

export const PAIR_ADDED = 1;
export const PAIR_REMOVED = 2;
export const PAIR_CHANGED = 4;

export function getPairFlag(type: EventType): number {
    return type === 'add' ? PAIR_ADDED : type === 'remove' ? PAIR_REMOVED : PAIR_CHANGED;
}

export function getPairLog(world: World, id: number): PairState {
    const logs = world[$internal].pairTrackingLogs;
    let log = logs.get(id);
    if (!log) {
        log = new Map();
        logs.set(id, log);
    }
    return log;
}

/** Records a pair event. Opposite add/remove events on the same target cancel. */
function applyPairEvent(
    state: PairState,
    eid: number,
    traitId: number,
    target: number,
    type: EventType
) {
    let byTrait = state.get(eid);
    if (!byTrait) state.set(eid, (byTrait = new Map()));
    let byTarget = byTrait.get(traitId);
    if (!byTarget) byTrait.set(traitId, (byTarget = new Map()));

    let flags = byTarget.get(target) ?? 0;

    if (type === 'add') {
        flags = flags & PAIR_REMOVED ? flags & ~PAIR_REMOVED : flags | PAIR_ADDED;
        flags &= ~PAIR_CHANGED;
    } else if (type === 'remove') {
        flags = flags & PAIR_ADDED ? flags & ~PAIR_ADDED : flags | PAIR_REMOVED;
        flags &= ~PAIR_CHANGED;
    } else {
        flags |= PAIR_CHANGED;
    }

    if (flags === 0) {
        byTarget.delete(target);
        if (byTarget.size === 0) byTrait.delete(traitId);
        if (byTrait.size === 0) state.delete(eid);
    } else {
        byTarget.set(target, flags);
    }
}

export function emitPairEvent(
    world: World,
    entity: Entity,
    trait: Trait,
    target: Entity,
    type: EventType
) {
    const eid = getEntityId(entity);
    const traitId = trait.id;

    const cursor = getTrackingCursor();
    for (let id = FIRST_TRACKING_ID; id < cursor; id++) {
        applyPairEvent(getPairLog(world, id), eid, traitId, target, type);
    }

    const instance = getTraitInstance(world[$internal].traitInstances, trait);
    if (!instance) return;

    for (const query of instance.trackingQueries) {
        if (query.pairGroups.length === 0) continue;

        for (const state of query.pairState.values()) {
            applyPairEvent(state, eid, traitId, target, type);
        }

        if (checkQueryTrackingWithRelations(world, query, entity, type, -1, 0)) query.add(entity);
        else query.remove(world, entity);
    }
}

export function matchesPairGroup(query: QueryInstance, group: PairGroup, eid: number): boolean {
    const byTrait = query.pairState.get(group.id)?.get(eid);
    if (!byTrait) return false;
    const byTarget = byTrait.get(group.trait.id);
    if (!byTarget) return false;

    const flag = getPairFlag(group.type);
    const target = group.target;

    if (target === '*') {
        for (const flags of byTarget.values()) {
            if (flags & flag) return true;
        }
        return false;
    }

    return ((byTarget.get(target) ?? 0) & flag) !== 0;
}

/** Seeds a query's pair state from the world log for each tracking id it uses. */
export function seedPairState(world: World, query: QueryInstance) {
    for (const group of query.pairGroups) {
        if (query.pairState.has(group.id)) continue;
        const copy: PairState = new Map();
        for (const [eid, byTrait] of getPairLog(world, group.id)) {
            const traitsCopy = new Map<number, Map<number, number>>();
            for (const [traitId, byTarget] of byTrait) traitsCopy.set(traitId, new Map(byTarget));
            copy.set(eid, traitsCopy);
        }
        query.pairState.set(group.id, copy);
    }
}

/**
 * Evaluates pair groups for an entity.
 * Returns whether all AND pair groups match, and whether any OR pair group matched.
 */
export function evaluatePairGroups(
    query: QueryInstance,
    eid: number
): { and: boolean; hasOr: boolean; anyOr: boolean } {
    let hasOr = false;
    let anyOr = false;

    for (const group of query.pairGroups) {
        if (group.logic === 'or') {
            hasOr = true;
            if (!anyOr && matchesPairGroup(query, group, eid)) anyOr = true;
        } else if (!matchesPairGroup(query, group, eid)) {
            return { and: false, hasOr, anyOr };
        }
    }

    return { and: true, hasOr, anyOr };
}

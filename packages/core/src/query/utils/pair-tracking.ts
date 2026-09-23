import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import type { TraitInstance } from '../../trait/types';
import type { World } from '../../world';
import type { EventType, PairEventLog, PairTrackingGroup, QueryInstance } from '../types';
import { checkQueryTrackingWithRelations } from './check-query-tracking-with-relations';

const PAIR_ADDED = 1;
const PAIR_REMOVED = 2;
const PAIR_CHANGED = 4;

/* @inline @pure */ function getPairEventFlag(type: EventType): number {
    return type === 'add' ? PAIR_ADDED : type === 'remove' ? PAIR_REMOVED : PAIR_CHANGED;
}

/**
 * Record a pair event as net flags for the pair. An add and a remove of the
 * same pair cancel each other out, and either one discards a pending change.
 */
export function recordPairEvent(
    log: PairEventLog,
    eid: number,
    traitId: number,
    target: number,
    type: EventType
): void {
    let byTrait = log.get(eid);
    if (!byTrait) {
        byTrait = new Map();
        log.set(eid, byTrait);
    }

    let byTarget = byTrait.get(traitId);
    if (!byTarget) {
        byTarget = new Map();
        byTrait.set(traitId, byTarget);
    }

    const flags = byTarget.get(target) ?? 0;
    let next: number;

    if (type === 'add') next = flags & PAIR_REMOVED ? 0 : PAIR_ADDED;
    else if (type === 'remove') next = flags & PAIR_ADDED ? 0 : PAIR_REMOVED;
    else next = flags | PAIR_CHANGED;

    if (next !== 0) {
        byTarget.set(target, next);
        return;
    }

    byTarget.delete(target);
    if (byTarget.size === 0) byTrait.delete(traitId);
    if (byTrait.size === 0) log.delete(eid);
}

/** Check whether an entity satisfies a pair tracking group */
export function checkPairTrackingGroup(group: PairTrackingGroup, eid: number): boolean {
    const byTrait = group.events.get(eid);
    const flag = getPairEventFlag(group.type);
    const isAnd = group.logic === 'and';

    for (const { traitId, target } of group.pairs) {
        let matched = false;
        const byTarget = byTrait?.get(traitId);

        if (byTarget) {
            if (target === '*') {
                for (const flags of byTarget.values()) {
                    if (flags & flag) {
                        matched = true;
                        break;
                    }
                }
            } else {
                matched = ((byTarget.get(target) ?? 0) & flag) !== 0;
            }
        }

        if (isAnd && !matched) return false;
        if (!isAnd && matched) return true;
    }

    return isAnd;
}

/** Seed a pair tracking group with the events recorded by the world since its modifier was created */
export function seedPairTrackingGroup(world: World, group: PairTrackingGroup): void {
    const log = world[$internal].pairEventLogs.get(group.id);
    if (!log) return;

    for (const [eid, byTrait] of log) {
        for (const [traitId, byTarget] of byTrait) {
            if (!group.traitIds.has(traitId)) continue;
            for (const [target, flags] of byTarget) {
                let groupByTrait = group.events.get(eid);
                if (!groupByTrait) {
                    groupByTrait = new Map();
                    group.events.set(eid, groupByTrait);
                }
                let groupByTarget = groupByTrait.get(traitId);
                if (!groupByTarget) {
                    groupByTarget = new Map();
                    groupByTrait.set(traitId, groupByTarget);
                }
                groupByTarget.set(target, flags);
            }
        }
    }
}

export function resetPairTracking(query: QueryInstance, eid: number): void {
    const groups = query.pairTrackingGroups;
    for (let i = 0; i < groups.length; i++) {
        groups[i].events.delete(eid);
    }
}

/**
 * Close the observation window for entities that are not in the query. Only each
 * group's own event flag survives so partial AND matches carry over, while opposite
 * events from the closed window can no longer cancel future ones.
 */
export function prunePairTracking(query: QueryInstance): void {
    const groups = query.pairTrackingGroups;
    if (groups.length === 0) return;

    const keep = new Set<number>();
    const dense = query.entities.dense;
    for (let i = 0; i < dense.length; i++) keep.add(getEntityId(dense[i] as Entity));

    for (let i = 0; i < groups.length; i++) {
        const group = groups[i];
        const flag = getPairEventFlag(group.type);

        for (const [eid, byTrait] of group.events) {
            if (keep.has(eid)) continue;
            for (const [traitId, byTarget] of byTrait) {
                for (const [target, flags] of byTarget) {
                    if (flags & flag) byTarget.set(target, flag);
                    else byTarget.delete(target);
                }
                if (byTarget.size === 0) byTrait.delete(traitId);
            }
            if (byTrait.size === 0) group.events.delete(eid);
        }
    }
}

/**
 * Emit a pair-level event (add, remove or change of a single relation target)
 * to every tracking modifier log and to queries tracking pairs of this relation.
 */
export function emitPairEvent(
    world: World,
    instance: TraitInstance,
    entity: Entity,
    target: Entity,
    type: EventType
): void {
    const ctx = world[$internal];
    const eid = getEntityId(entity);
    const traitId = instance.trait.id;

    for (const log of ctx.pairEventLogs.values()) {
        recordPairEvent(log, eid, traitId, target, type);
    }

    for (const query of instance.pairTrackingQueries) {
        const groups = query.pairTrackingGroups;
        for (let i = 0; i < groups.length; i++) {
            const group = groups[i];
            if (group.traitIds.has(traitId))
                recordPairEvent(group.events, eid, traitId, target, type);
        }

        // A zero bitflag re-evaluates the query without touching trait-level trackers.
        const match = checkQueryTrackingWithRelations(world, query, entity, type, 0, 0);
        if (match) query.add(entity);
        else query.remove(world, entity);
    }
}

import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import { getRelationTargets } from '../../relation/relation';
import type { Relation } from '../../relation/types';
import { getTraitInstance } from '../../trait/trait-instance';
import type { Trait } from '../../trait/types';
import type { World } from '../../world';
import type { EventType, PairSnapshot, QueryInstance, TrackingGroup } from '../types';
import { checkQueryTrackingState } from './check-query-tracking-with-relations';

// Per-target states. Added and removed are net events, so opposite events on a target cancel out.
const PAIR_ADDED = 1;
const PAIR_REMOVED = -1;
const PAIR_CHANGED = 2;

const EMPTY_TARGETS: readonly Entity[] = [];

/** Capture every relation pair in the world so later pair additions and removals can be diffed against it. */
export function createPairSnapshot(world: World): PairSnapshot {
    const ctx = world[$internal];
    const { dense, sparse } = ctx.entityIndex;
    const targets: PairSnapshot['targets'] = [];

    for (const relation of ctx.relations) {
        const trait = relation[$internal].trait;
        const relationTargets = getTraitInstance(ctx.traitInstances, trait)?.relationTargets;
        if (!relationTargets) continue;

        const bySource = new Map<Entity, readonly Entity[]>();

        for (let eid = 0; eid < relationTargets.length; eid++) {
            if (relationTargets[eid] === undefined) continue;
            const denseIndex = sparse[eid];
            if (denseIndex === undefined) continue;

            const source = dense[denseIndex];
            const sourceTargets = getRelationTargets(world, relation, source);
            if (sourceTargets.length > 0) bySource.set(source, sourceTargets);
        }

        targets[trait.id] = bySource;
    }

    return { tick: ctx.pairTick, targets };
}

function getPairState(type: EventType): number {
    return type === 'add' ? PAIR_ADDED : type === 'remove' ? PAIR_REMOVED : PAIR_CHANGED;
}

function applyPairEvent(
    states: Map<Entity, number>,
    target: Entity,
    groupType: EventType,
    eventType: EventType
) {
    const prev = states.get(target);
    let next: number | undefined;

    if (groupType === 'change') {
        // A removed pair can no longer count as changed.
        next = eventType === 'change' ? PAIR_CHANGED : eventType === 'remove' ? undefined : prev;
    } else if (eventType === 'add') {
        next = prev === PAIR_REMOVED ? undefined : PAIR_ADDED;
    } else {
        next = prev === PAIR_ADDED ? undefined : PAIR_REMOVED;
    }

    if (next === undefined) states.delete(target);
    else states.set(target, next);
}

function getEntityPairStates(group: TrackingGroup, eid: number) {
    let entityStates = group.pairTrackers.get(eid);
    if (!entityStates) {
        entityStates = [];
        group.pairTrackers.set(eid, entityStates);
    }
    return entityStates;
}

/**
 * Record a pair event for tracking queries that track pairs of the relation
 * and update their membership for the entity.
 */
export function trackPairEvent(
    world: World,
    relation: Relation<Trait>,
    entity: Entity,
    target: Entity,
    eventType: EventType
) {
    const ctx = world[$internal];
    const instance = getTraitInstance(ctx.traitInstances, relation[$internal].trait);
    if (!instance) return;

    const eid = getEntityId(entity);

    // Kept for tracking queries created later, which diff against their modifier's snapshot tick.
    if (eventType === 'change') {
        const ticks = (instance.pairChangeTicks ??= []);
        (ticks[eid] ??= new Map()).set(target, ++ctx.pairTick);
    } else if (eventType === 'remove') {
        instance.pairChangeTicks?.[eid]?.delete(target);
    }

    for (const query of instance.pairTrackingQueries) {
        const groups = query.trackingGroups;
        let tracked = false;

        for (let i = 0; i < groups.length; i++) {
            const group = groups[i];
            const pairs = group.pairs;
            if (pairs.length === 0) continue;
            if (eventType === 'change' && group.type !== 'change') continue;

            for (let j = 0; j < pairs.length; j++) {
                const pairCtx = pairs[j][$internal];
                if (pairCtx.relation !== relation) continue;
                if (pairCtx.target !== '*' && pairCtx.target !== target) continue;

                const entityStates = getEntityPairStates(group, eid);
                applyPairEvent((entityStates[j] ??= new Map()), target, group.type, eventType);
                tracked = true;
            }
        }

        if (!tracked) continue;

        if (checkQueryTrackingState(world, query, entity)) query.add(entity);
        else query.remove(world, entity);
    }
}

/**
 * Seed an entity's pair trackers with the pair events that happened since the group's
 * modifier started observing the world. Used when a tracking query is created.
 */
export function seedPairTrackers(world: World, group: TrackingGroup, entity: Entity) {
    const ctx = world[$internal];
    const snapshot = ctx.pairSnapshots.get(group.id);
    if (!snapshot) return;

    const eid = getEntityId(entity);
    const pairs = group.pairs;

    for (let i = 0; i < pairs.length; i++) {
        const { relation, target } = pairs[i][$internal];
        const trait = relation[$internal].trait;

        if (group.type === 'change') {
            const ticks = getTraitInstance(ctx.traitInstances, trait)?.pairChangeTicks?.[eid];
            if (!ticks) continue;
            for (const [t, tick] of ticks) {
                if (tick > snapshot.tick && (target === '*' || target === t)) {
                    seedPairState(group, eid, i, t, PAIR_CHANGED);
                }
            }
        } else {
            const current = getRelationTargets(world, relation, entity);
            const previous = snapshot.targets[trait.id]?.get(entity) ?? EMPTY_TARGETS;
            for (const t of current) {
                if ((target === '*' || target === t) && !previous.includes(t)) {
                    seedPairState(group, eid, i, t, PAIR_ADDED);
                }
            }
            for (const t of previous) {
                if ((target === '*' || target === t) && !current.includes(t)) {
                    seedPairState(group, eid, i, t, PAIR_REMOVED);
                }
            }
        }
    }
}

function seedPairState(
    group: TrackingGroup,
    eid: number,
    index: number,
    target: Entity,
    state: number
) {
    const entityStates = getEntityPairStates(group, eid);
    (entityStates[index] ??= new Map()).set(target, state);
}

/**
 * End the observation window of a query's pair trackers. Events that don't satisfy their group
 * only exist to cancel opposite events within the window, so they are dropped.
 */
export function prunePairTrackers(query: QueryInstance) {
    const groups = query.trackingGroups;

    for (let i = 0; i < groups.length; i++) {
        const group = groups[i];
        if (group.pairs.length === 0) continue;

        const state = getPairState(group.type);

        for (const [eid, entityStates] of group.pairTrackers) {
            let hasStates = false;

            for (let j = 0; j < entityStates.length; j++) {
                const states = entityStates[j];
                if (!states) continue;

                for (const [target, value] of states) {
                    if (value !== state) states.delete(target);
                }

                if (states.size > 0) hasStates = true;
                else entityStates[j] = undefined;
            }

            if (!hasStates) group.pairTrackers.delete(eid);
        }
    }
}

/** Check the tracked pairs of a group for an entity. AND groups need every pair, OR groups any pair. */
export function isPairGroupTracked(group: TrackingGroup, eid: number): boolean {
    const entityStates = group.pairTrackers.get(eid);
    if (!entityStates) return false;

    const state = getPairState(group.type);
    const isAnd = group.logic === 'and';
    const pairsLen = group.pairs.length;

    for (let i = 0; i < pairsLen; i++) {
        const tracked = hasPairState(entityStates[i], state);
        if (isAnd && !tracked) return false;
        if (!isAnd && tracked) return true;
    }

    return isAnd;
}

function hasPairState(states: Map<Entity, number> | undefined, state: number): boolean {
    if (!states) return false;
    for (const value of states.values()) {
        if (value === state) return true;
    }
    return false;
}

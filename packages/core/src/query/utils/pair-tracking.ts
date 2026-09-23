import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import { getRelationTargets, hasRelationToTarget } from '../../relation/relation';
import type { Relation, RelationTarget } from '../../relation/types';
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

// Change ticks are only read by Changed modifiers, which ignore changes made before they exist.
let recordPairChangeTicks = false;

export function enablePairChangeTicks() {
    recordPairChangeTicks = true;
}

/** A pair matched by a tracking modifier when its query ran */
export type PairMatch = {
    target: Entity;
    /** The pair's data when it was removed, for pairs tracked by `Removed` */
    data: unknown;
};

/** Matched pairs per entity, keyed by `getPairKey` */
export type PairMatches = Map<string, Map<Entity, PairMatch>>;

export function getPairKey(modifierId: number, relationTraitId: number, target: RelationTarget) {
    return `${modifierId}:${relationTraitId}:${target}`;
}

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

function getNextPairState(
    prev: number | undefined,
    groupType: EventType,
    eventType: EventType
): number | undefined {
    // A removed pair can no longer count as changed.
    if (groupType === 'change') return eventType === 'change' ? PAIR_CHANGED : undefined;
    if (eventType === 'add') return prev === PAIR_REMOVED ? undefined : PAIR_ADDED;
    return prev === PAIR_ADDED ? undefined : PAIR_REMOVED;
}

function getPairStates(group: TrackingGroup, eid: number, index: number) {
    let entityStates = group.pairTrackers.get(eid);
    if (!entityStates) {
        entityStates = [];
        group.pairTrackers.set(eid, entityStates);
    }
    return (entityStates[index] ??= new Map());
}

function getRemovedPairData(group: TrackingGroup, eid: number, index: number) {
    let entityData = group.removedPairData.get(eid);
    if (!entityData) {
        entityData = [];
        group.removedPairData.set(eid, entityData);
    }
    return (entityData[index] ??= new Map());
}

/**
 * Record a pair event for tracking queries that track the pair and update their membership
 * for the entity. For removals, `removedData` is the pair's data before it was removed.
 */
export function trackPairEvent(
    world: World,
    relation: Relation<Trait>,
    entity: Entity,
    target: Entity,
    eventType: EventType,
    removedData?: unknown
) {
    const ctx = world[$internal];
    const instance = getTraitInstance(ctx.traitInstances, relation[$internal].trait);
    if (!instance) return;

    const eid = getEntityId(entity);

    // Kept for tracking queries created later, which diff against their modifier's snapshot tick.
    if (eventType === 'change') {
        if (recordPairChangeTicks) {
            const ticks = (instance.pairChangeTicks ??= []);
            (ticks[eid] ??= new Map()).set(target, ++ctx.pairTick);
        }
    } else {
        // An added or removed pair starts over without changes.
        instance.pairChangeTicks?.[eid]?.delete(target);
    }

    const index = instance.pairTrackingQueries;
    if (index.size === 0) return;

    const specificQueries = index.get(target);
    const wildcardQueries = index.get('*');
    if (!specificQueries && !wildcardQueries) return;

    if (eventType === 'change' && !hasRelationToTarget(world, relation, entity, target)) return;

    if (specificQueries) {
        for (const query of specificQueries) {
            trackQueryPairEvent(world, query, relation, entity, target, eventType, removedData);
        }
    }

    if (wildcardQueries) {
        for (const query of wildcardQueries) {
            trackQueryPairEvent(world, query, relation, entity, target, eventType, removedData);
        }
    }
}

function trackQueryPairEvent(
    world: World,
    query: QueryInstance,
    relation: Relation<Trait>,
    entity: Entity,
    target: Entity,
    eventType: EventType,
    removedData: unknown
) {
    const groups = query.trackingGroups;
    const eid = getEntityId(entity);
    let tracked = false;

    for (let i = 0; i < groups.length; i++) {
        const group = groups[i];
        const groupType = group.type;
        const pairs = group.pairs;
        if (pairs.length === 0) continue;

        // Changes only matter to Changed groups, and additions never affect them.
        if (groupType === 'change' ? eventType === 'add' : eventType === 'change') continue;

        for (let j = 0; j < pairs.length; j++) {
            const pairCtx = pairs[j][$internal];
            if (pairCtx.relation !== relation) continue;
            if (pairCtx.target !== '*' && pairCtx.target !== target) continue;

            const states = group.pairTrackers.get(eid)?.[j];
            const prev = states?.get(target);
            const next = getNextPairState(prev, groupType, eventType);
            if (next === prev) continue;

            if (next === undefined) states!.delete(target);
            else getPairStates(group, eid, j).set(target, next);

            if (groupType === 'remove') {
                if (next === PAIR_REMOVED && removedData !== undefined) {
                    getRemovedPairData(group, eid, j).set(target, removedData);
                } else {
                    group.removedPairData.get(eid)?.[j]?.delete(target);
                }
            }

            tracked = true;
        }
    }

    if (!tracked) return;

    if (checkQueryTrackingState(world, query, entity)) query.add(entity);
    else query.remove(world, entity);
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
                if (tick <= snapshot.tick || (target !== '*' && target !== t)) continue;
                if (hasRelationToTarget(world, relation, entity, t)) {
                    getPairStates(group, eid, i).set(t, PAIR_CHANGED);
                }
            }
        } else {
            const current = getRelationTargets(world, relation, entity);
            const previous = snapshot.targets[trait.id]?.get(entity) ?? EMPTY_TARGETS;
            for (const t of current) {
                if ((target === '*' || target === t) && !previous.includes(t)) {
                    getPairStates(group, eid, i).set(t, PAIR_ADDED);
                }
            }
            for (const t of previous) {
                if ((target === '*' || target === t) && !current.includes(t)) {
                    getPairStates(group, eid, i).set(t, PAIR_REMOVED);
                }
            }
        }
    }
}

/**
 * Find the pairs matched by the entities a query returns, for iterating their data. Specific
 * targets are only needed for removed pairs, whose data is no longer stored. Must run before
 * the trackers of the entities are reset.
 */
export function resolvePairMatches(
    query: QueryInstance,
    entities: readonly Entity[]
): PairMatches | undefined {
    let matches: PairMatches | undefined;
    const groups = query.trackingGroups;

    for (let i = 0; i < groups.length; i++) {
        const group = groups[i];
        // Only top level modifiers contribute traits to iterate.
        if (group.logic !== 'and') continue;

        const pairs = group.pairs;
        const state = getPairState(group.type);

        for (let j = 0; j < pairs.length; j++) {
            const { relation, target } = pairs[j][$internal];
            if (target !== '*' && group.type !== 'remove') continue;

            const key = getPairKey(group.id, relation[$internal].trait.id, target);
            if (matches?.has(key)) continue;

            const byEntity = new Map<Entity, PairMatch>();
            for (let k = 0; k < entities.length; k++) {
                const entity = entities[k];
                const eid = getEntityId(entity);
                const states = group.pairTrackers.get(eid)?.[j];
                if (!states) continue;
                for (const [t, value] of states) {
                    if (value !== state) continue;
                    byEntity.set(entity, {
                        target: t,
                        data: group.removedPairData.get(eid)?.[j]?.get(t),
                    });
                    break;
                }
            }

            (matches ??= new Map()).set(key, byEntity);
        }
    }

    return matches;
}

/** Clear the pair trackers of an entity in a tracking group */
export function resetPairTrackers(group: TrackingGroup, eid: number) {
    group.pairTrackers.delete(eid);
    group.removedPairData.delete(eid);
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

            if (!hasStates) resetPairTrackers(group, eid);
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

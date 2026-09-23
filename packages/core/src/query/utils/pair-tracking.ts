import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import type { Relation, RelationPair } from '../../relation/types';
import { getTraitInstance } from '../../trait/trait-instance';
import type { Trait } from '../../trait/types';
import type { World } from '../../world';
import type { EventType, PairFilter, QueryInstance, TrackingGroup } from '../types';
import { getTrackingCursor } from './tracking-cursor';

/** Pair-event bits. Add and remove of the same target in one window clear each other. */
export const PAIR_ADD = 1;
export const PAIR_REMOVE = 2;
export const PAIR_CHANGE = 4;

const POSITIVE = PAIR_ADD | PAIR_REMOVE | PAIR_CHANGE;

export function positiveFlag(type: EventType): number {
    if (type === 'add') return PAIR_ADD;
    if (type === 'remove') return PAIR_REMOVE;
    return PAIR_CHANGE;
}

/**
 * Net pair flags for one target.
 * An add cancels a pending remove (and the reverse). The events drop out of the window.
 * Change sticks unless a removal or a cancellation clears it.
 */
export function applyPairFlags(flags: number, event: EventType): number {
    if (event === 'add') {
        if (flags & PAIR_REMOVE) return 0;
        return flags | PAIR_ADD;
    }
    if (event === 'remove') {
        if (flags & PAIR_ADD) return 0;
        return PAIR_REMOVE;
    }
    if (flags & PAIR_REMOVE) return flags;
    return flags | PAIR_CHANGE;
}

function groupHasBitmasks(group: TrackingGroup): boolean {
    const masks = group.bitmasks;
    for (let i = 0; i < masks.length; i++) {
        if (masks[i]) return true;
    }
    return false;
}

function bitmasksSatisfied(group: TrackingGroup, entity: Entity): boolean {
    const eid = getEntityId(entity);
    const masks = group.bitmasks;
    const trackers = group.trackers;

    if (group.logic === 'or') {
        for (let genId = 0; genId < masks.length; genId++) {
            const mask = masks[genId];
            if (!mask) continue;
            const trackerArr = trackers[genId];
            const tracker = trackerArr ? trackerArr[eid] | 0 : 0;
            if (tracker & mask) return true;
        }
        return false;
    }

    for (let genId = 0; genId < masks.length; genId++) {
        const mask = masks[genId];
        if (!mask) continue;
        const trackerArr = trackers[genId];
        const tracker = trackerArr ? trackerArr[eid] | 0 : 0;
        if ((tracker & mask) !== mask) return false;
    }
    return true;
}

function targetHasFlag(
    group: TrackingGroup,
    entity: Entity,
    traitId: number,
    target: PairFilter['target'],
    flag: number
): boolean {
    const byTrait = group.pairState?.get(entity);
    const byTarget = byTrait?.get(traitId);
    if (!byTarget) return false;

    if (target === '*') {
        for (const flags of byTarget.values()) {
            if (flags & flag) return true;
        }
        return false;
    }

    return ((byTarget.get(target) ?? 0) & flag) !== 0;
}

function pairsSatisfied(group: TrackingGroup, entity: Entity): boolean {
    const pairs = group.pairs;
    if (!pairs || pairs.length === 0) return true;

    const flag = positiveFlag(group.type);
    if (group.logic === 'or') {
        for (let i = 0; i < pairs.length; i++) {
            const pair = pairs[i];
            if (targetHasFlag(group, entity, pair.traitId, pair.target, flag)) return true;
        }
        return false;
    }

    for (let i = 0; i < pairs.length; i++) {
        const pair = pairs[i];
        if (!targetHasFlag(group, entity, pair.traitId, pair.target, flag)) return false;
    }
    return true;
}

/** Trait-tracker bits and pair flags for one tracking group. */
export function trackingGroupSatisfied(group: TrackingGroup, entity: Entity): boolean {
    const hasPairs = !!group.pairs?.length;
    const hasBits = groupHasBitmasks(group);

    if (group.logic === 'or') {
        if (hasBits && bitmasksSatisfied(group, entity)) return true;
        if (hasPairs && pairsSatisfied(group, entity)) return true;
        return false;
    }

    if (hasBits && !bitmasksSatisfied(group, entity)) return false;
    if (hasPairs && !pairsSatisfied(group, entity)) return false;
    return true;
}

function passesStaticConstraints(world: World, query: QueryInstance, entity: Entity): boolean {
    const generations = query.generations;
    const staticBitmasks = query.staticBitmasks;
    const entityMasks = world[$internal].entityMasks;
    const eid = getEntityId(entity);

    if (query.traitInstances.all.length === 0) return false;

    for (let i = 0; i < generations.length; i++) {
        const generationId = generations[i];
        const bitmask = staticBitmasks[i];
        if (!bitmask) continue;

        const required = bitmask.required;
        const forbidden = bitmask.forbidden;
        const or = bitmask.or;
        const genMasks = entityMasks[generationId];
        const entityMask = genMasks ? genMasks[eid] | 0 : 0;

        if (forbidden && (entityMask & forbidden) !== 0) return false;
        if (required && (entityMask & required) !== required) return false;
        if (or !== 0 && (entityMask & or) === 0) return false;
    }

    return true;
}

function entityHasPair(world: World, entity: Entity, pair: RelationPair): boolean {
    const pairCtx = pair[$internal];
    const relation = pairCtx.relation;
    const relationTrait = relation[$internal].trait;
    const inst = getTraitInstance(world[$internal].traitInstances, relationTrait);
    if (!inst) return false;

    const eid = getEntityId(entity);
    const mask = world[$internal].entityMasks[inst.generationId]?.[eid] ?? 0;
    if ((mask & inst.bitflag) !== inst.bitflag) return false;

    const target = pairCtx.target;
    if (target === '*') return true;
    if (!inst.relationTargets) return false;

    if (relation[$internal].exclusive) {
        return (inst.relationTargets as Array<Entity | undefined>)[eid] === target;
    }

    const targets = (inst.relationTargets as number[][])[eid];
    return targets ? targets.includes(target as number) : false;
}

function passesRelationFilters(world: World, query: QueryInstance, entity: Entity): boolean {
    const filters = query.relationFilters;
    if (!filters || filters.length === 0) return true;
    for (let i = 0; i < filters.length; i++) {
        if (!entityHasPair(world, entity, filters[i])) return false;
    }
    return true;
}

function traitGroupMatchesSnapshot(world: World, group: TrackingGroup, entity: Entity): boolean {
    const ctx = world[$internal];
    const eid = getEntityId(entity);
    const snapshot = ctx.trackingSnapshots.get(group.id);
    const dirtyMask = ctx.dirtyMasks.get(group.id);
    const changedMask = ctx.changedMasks.get(group.id);
    if (!snapshot || !dirtyMask || !changedMask) return false;

    const masks = group.bitmasks;
    let saw = false;

    for (let genId = 0; genId < masks.length; genId++) {
        const mask = masks[genId];
        if (!mask) continue;
        saw = true;

        const oldMask = snapshot[genId]?.[eid] || 0;
        const currentMask = ctx.entityMasks[genId]?.[eid] || 0;

        for (let bit = 1; bit <= mask; bit <<= 1) {
            if (!(mask & bit)) continue;

            let traitMatches = false;
            switch (group.type) {
                case 'add':
                    traitMatches = (oldMask & bit) === 0 && (currentMask & bit) === bit;
                    break;
                case 'remove':
                    traitMatches =
                        ((oldMask & bit) === bit && (currentMask & bit) === 0) ||
                        ((oldMask & bit) === 0 &&
                            (currentMask & bit) === 0 &&
                            ((dirtyMask[genId]?.[eid] ?? 0) & bit) === bit);
                    break;
                case 'change':
                    traitMatches = ((changedMask[genId]?.[eid] ?? 0) & bit) === bit;
                    break;
            }

            if (group.logic === 'and') {
                if (!traitMatches) return false;
            } else if (traitMatches) {
                return true;
            }
        }
    }

    if (!saw) return group.logic === 'and';
    return group.logic === 'and';
}

function initialGroupMatches(world: World, group: TrackingGroup, entity: Entity): boolean {
    const hasPairs = !!group.pairs?.length;
    const hasBits = groupHasBitmasks(group);
    const bits = hasBits && traitGroupMatchesSnapshot(world, group, entity);
    const pairs = hasPairs && pairsSatisfied(group, entity);

    if (group.logic === 'or') return bits || pairs;
    if (hasBits && !bits) return false;
    if (hasPairs && !pairs) return false;
    return hasBits || hasPairs;
}

export function queryHasPairTracking(query: QueryInstance): boolean {
    const groups = query.trackingGroups;
    for (let i = 0; i < groups.length; i++) {
        if (groups[i].pairs?.length) return true;
    }
    return false;
}

function groupsMatch(
    world: World,
    query: QueryInstance,
    entity: Entity,
    mode: 'initial' | 'current'
): boolean {
    const groups = query.trackingGroups;
    let hasOr = false;
    let anyOr = false;

    for (let i = 0; i < groups.length; i++) {
        const group = groups[i];
        const ok =
            mode === 'initial'
                ? initialGroupMatches(world, group, entity)
                : trackingGroupSatisfied(group, entity);

        if (group.logic === 'or') {
            hasOr = true;
            if (ok) anyOr = true;
        } else if (!ok) {
            return false;
        }
    }

    if (hasOr && !anyOr) return false;
    return groups.length > 0;
}

export function entityMatchesPairQuery(
    world: World,
    query: QueryInstance,
    entity: Entity,
    mode: 'initial' | 'current'
): boolean {
    if (!passesStaticConstraints(world, query, entity)) return false;
    if (!passesRelationFilters(world, query, entity)) return false;
    return groupsMatch(world, query, entity, mode);
}

function ensureTargetMap(
    root: Map<Entity, Map<number, Map<Entity, number>>>,
    entity: Entity,
    traitId: number
): Map<Entity, number> {
    let byTrait = root.get(entity);
    if (!byTrait) {
        byTrait = new Map();
        root.set(entity, byTrait);
    }
    let byTarget = byTrait.get(traitId);
    if (!byTarget) {
        byTarget = new Map();
        byTrait.set(traitId, byTarget);
    }
    return byTarget;
}

function writeLogFlags(
    root: Map<Entity, Map<number, Map<Entity, number>>>,
    entity: Entity,
    traitId: number,
    target: Entity,
    event: EventType
): void {
    const byTarget = ensureTargetMap(root, entity, traitId);
    const next = applyPairFlags(byTarget.get(target) ?? 0, event);
    if (next === 0) byTarget.delete(target);
    else byTarget.set(target, next);
}

function cacheRemovedData(
    world: World,
    entity: Entity,
    traitId: number,
    target: Entity,
    data: unknown
): void {
    if (data === undefined) return;
    const ctx = world[$internal];
    let byTrait = ctx.removedPairData.get(entity);
    if (!byTrait) {
        byTrait = new Map();
        ctx.removedPairData.set(entity, byTrait);
    }
    let byTarget = byTrait.get(traitId);
    if (!byTarget) {
        byTarget = new Map();
        byTrait.set(traitId, byTarget);
    }
    const snapshot =
        data !== null && typeof data === 'object' ? { ...(data as Record<string, unknown>) } : data;
    byTarget.set(target, snapshot);
}

function clearRemovedData(world: World, entity: Entity, traitId: number, target: Entity): void {
    const byTrait = world[$internal].removedPairData.get(entity);
    const byTarget = byTrait?.get(traitId);
    if (!byTarget) return;
    byTarget.delete(target);
}

export function getRemovedPairData(
    world: World,
    entity: Entity,
    traitId: number,
    target: Entity
): unknown {
    return world[$internal].removedPairData.get(entity)?.get(traitId)?.get(target);
}

function refreshFocus(
    query: QueryInstance,
    entity: Entity,
    traitId: number,
    target: Entity,
    flags: number,
    group: TrackingGroup
): void {
    if (!query.pairFocus) query.pairFocus = new Map();
    let byTrait = query.pairFocus.get(entity);

    if (flags & POSITIVE) {
        if (!byTrait) {
            byTrait = new Map();
            query.pairFocus.set(entity, byTrait);
        }
        byTrait.set(traitId, target);
        return;
    }

    if (byTrait?.get(traitId) !== target) return;

    // This target cancelled. Point at another target that still has a positive flag.
    const byTarget = group.pairState?.get(entity)?.get(traitId);
    if (byTarget) {
        for (const [other, otherFlags] of byTarget) {
            if (otherFlags & POSITIVE) {
                byTrait.set(traitId, other);
                return;
            }
        }
    }
    byTrait.delete(traitId);
    if (byTrait.size === 0) query.pairFocus.delete(entity);
}

function writeGroupFlags(
    query: QueryInstance,
    group: TrackingGroup,
    entity: Entity,
    traitId: number,
    target: Entity,
    event: EventType
): void {
    if (!group.pairState) group.pairState = new Map();
    const byTarget = ensureTargetMap(group.pairState, entity, traitId);
    const next = applyPairFlags(byTarget.get(target) ?? 0, event);
    if (next === 0) byTarget.delete(target);
    else byTarget.set(target, next);
    refreshFocus(query, entity, traitId, target, next, group);
}

function groupCaresAbout(group: TrackingGroup, traitId: number, target: Entity): boolean {
    const pairs = group.pairs;
    if (!pairs) return false;
    for (let i = 0; i < pairs.length; i++) {
        const pair = pairs[i];
        if (pair.traitId !== traitId) continue;
        if (pair.target === '*' || pair.target === target) return true;
    }
    return false;
}

function copyFlags(
    group: TrackingGroup,
    entity: Entity,
    traitId: number,
    target: Entity,
    flags: number
): void {
    if (!flags) return;
    if (!group.pairState) group.pairState = new Map();
    ensureTargetMap(group.pairState, entity, traitId).set(target, flags);
}

/** Copy pair events observed since the tracking modifier was created into a new query. */
export function seedPairGroups(world: World, query: QueryInstance): void {
    const logs = world[$internal].pairEvents;
    const groups = query.trackingGroups;

    for (let g = 0; g < groups.length; g++) {
        const group = groups[g];
        if (!group.pairs?.length) continue;
        const log = logs.get(group.id);
        if (!log) continue;

        for (const [entity, byTrait] of log) {
            for (let p = 0; p < group.pairs.length; p++) {
                const pair = group.pairs[p];
                const byTarget = byTrait.get(pair.traitId);
                if (!byTarget) continue;

                if (pair.target === '*') {
                    for (const [target, flags] of byTarget) {
                        copyFlags(group, entity, pair.traitId, target, flags);
                        if (flags & POSITIVE) refreshFocus(query, entity, pair.traitId, target, flags, group);
                    }
                } else {
                    const flags = byTarget.get(pair.target) ?? 0;
                    if (!flags) continue;
                    copyFlags(group, entity, pair.traitId, pair.target, flags);
                    if (flags & POSITIVE) refreshFocus(query, entity, pair.traitId, pair.target, flags, group);
                }
            }
        }
    }
}

/**
 * Record a pair add, remove, or change for every live tracking modifier.
 * Opposite add/remove events on the same target cancel inside the current window.
 */
export function recordPairEvent(
    world: World,
    entity: Entity,
    relation: Relation<Trait>,
    target: Entity,
    event: EventType,
    removedData?: unknown
): void {
    const trait = relation[$internal].trait;
    const traitId = trait.id;
    const ctx = world[$internal];
    const cursor = getTrackingCursor();

    for (let id = 3; id < cursor; id++) {
        let log = ctx.pairEvents.get(id);
        if (!log) {
            log = new Map();
            ctx.pairEvents.set(id, log);
        }
        writeLogFlags(log, entity, traitId, target, event);
    }

    if (event === 'remove') cacheRemovedData(world, entity, traitId, target, removedData);
    else if (event === 'add') clearRemovedData(world, entity, traitId, target);

    const instance = getTraitInstance(ctx.traitInstances, trait);
    if (!instance) return;

    for (const query of instance.trackingQueries) {
        let touched = false;
        const groups = query.trackingGroups;
        for (let i = 0; i < groups.length; i++) {
            const group = groups[i];
            if (!groupCaresAbout(group, traitId, target)) continue;
            writeGroupFlags(query, group, entity, traitId, target, event);
            touched = true;
        }
        if (!touched) continue;

        if (entityMatchesPairQuery(world, query, entity, 'current')) query.add(entity);
        else query.remove(world, entity);
    }
}

/** Targets to read while iterating a drained tracking result. */
export function snapshotPairFocus(
    query: QueryInstance,
    entities: readonly Entity[]
): Map<Entity, Map<number, Entity>> | undefined {
    const focus = query.pairFocus;
    if (!focus || focus.size === 0) return undefined;

    const snap = new Map<Entity, Map<number, Entity>>();
    for (let i = 0; i < entities.length; i++) {
        const entity = entities[i];
        const byTrait = focus.get(entity);
        if (!byTrait || byTrait.size === 0) continue;
        snap.set(entity, new Map(byTrait));
    }
    return snap.size ? snap : undefined;
}

/**
 * Close the observation window for one query execution.
 * Emitted entities start clean. Unmatched entities keep only the positive flag
 * for each group so a partial AND can still complete, while cancel-tokens from
 * the opposite event do not leak into the next window.
 */
export function closePairObservationWindow(query: QueryInstance, emitted: readonly Entity[]): void {
    const emittedIds = new Set<number>();
    for (let i = 0; i < emitted.length; i++) emittedIds.add(emitted[i]);

    const groups = query.trackingGroups;
    for (let g = 0; g < groups.length; g++) {
        const group = groups[g];
        const state = group.pairState;
        if (!state) continue;
        const positive = positiveFlag(group.type);

        for (const [entity, byTrait] of state) {
            if (emittedIds.has(entity)) {
                state.delete(entity);
                continue;
            }

            for (const [traitId, byTarget] of byTrait) {
                for (const [target, flags] of byTarget) {
                    const kept = flags & positive;
                    if (kept) byTarget.set(target, kept);
                    else byTarget.delete(target);
                }
                if (byTarget.size === 0) byTrait.delete(traitId);
            }
            if (byTrait.size === 0) state.delete(entity);
        }
    }

    const focus = query.pairFocus;
    if (focus) {
        for (let i = 0; i < emitted.length; i++) focus.delete(emitted[i]);
    }
}

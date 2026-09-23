import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import type { TrackedRelationPair } from '../../relation/tracked-pair';
import type { Trait } from '../../trait/types';
import { getTraitInstance } from '../../trait/trait-instance';
import type { World } from '../../world';
import type { PairBucket, QueryInstance, TrackingGroup } from '../types';
import { checkQueryTrackingWithRelations } from './check-query-tracking-with-relations';

export type PairEventKind = 'add' | 'remove' | 'change';

function emptyBucket(): PairBucket {
    return { add: new Set(), remove: new Set(), change: new Set() };
}

function bucketFor(
    root: Map<number, Map<number, PairBucket>>,
    eid: number,
    traitId: number
): PairBucket {
    let byTrait = root.get(eid);
    if (!byTrait) {
        byTrait = new Map();
        root.set(eid, byTrait);
    }
    let bucket = byTrait.get(traitId);
    if (!bucket) {
        bucket = emptyBucket();
        byTrait.set(traitId, bucket);
    }
    return bucket;
}

/**
 * Opposite events on the same target within one bucket cancel.
 * Add cancels a pending remove and the reverse. Remove also drops a pending change.
 */
export function applyPairEvent(bucket: PairBucket, kind: PairEventKind, target: number): void {
    if (kind === 'add') {
        if (bucket.remove.has(target)) bucket.remove.delete(target);
        else bucket.add.add(target);
        return;
    }

    if (kind === 'remove') {
        bucket.change.delete(target);
        if (bucket.add.has(target)) bucket.add.delete(target);
        else bucket.remove.add(target);
        return;
    }

    if (bucket.remove.has(target)) return;
    bucket.change.add(target);
}

function cloneBucket(bucket: PairBucket): PairBucket {
    return {
        add: new Set(bucket.add),
        remove: new Set(bucket.remove),
        change: new Set(bucket.change),
    };
}

export function clonePairState(
    state: Map<number, Map<number, PairBucket>>
): Map<number, Map<number, PairBucket>> {
    const copy = new Map<number, Map<number, PairBucket>>();
    for (const [eid, byTrait] of state) {
        const traitCopy = new Map<number, PairBucket>();
        for (const [traitId, bucket] of byTrait) {
            traitCopy.set(traitId, cloneBucket(bucket));
        }
        copy.set(eid, traitCopy);
    }
    return copy;
}

function globalStateFor(world: World, trackingId: number): Map<number, Map<number, PairBucket>> {
    const ctx = world[$internal];
    let state = ctx.pairEvents.get(trackingId);
    if (!state) {
        state = new Map();
        ctx.pairEvents.set(trackingId, state);
    }
    return state;
}

export function clearPairEventsForEntity(world: World, eid: number): void {
    const ctx = world[$internal];
    for (const state of ctx.pairEvents.values()) state.delete(eid);

    for (const query of ctx.queriesHashMap.values()) {
        const groups = query.trackingGroups;
        for (let i = 0; i < groups.length; i++) groups[i].pairState?.delete(eid);
    }
}

export function seedQueryPairState(world: World, query: QueryInstance): void {
    const groups = query.trackingGroups;
    for (let i = 0; i < groups.length; i++) {
        const group = groups[i];
        if (!group.pairFilters?.length) continue;
        const global = world[$internal].pairEvents.get(group.id);
        const seeded = global ? clonePairState(global) : new Map();
        for (const byTrait of seeded.values()) {
            for (const bucket of byTrait.values()) {
                if (group.type !== 'add') bucket.add.clear();
                if (group.type !== 'remove') bucket.remove.clear();
                if (group.type !== 'change') bucket.change.clear();
            }
        }
        group.pairState = seeded;
    }
}

function groupTracksTrait(group: TrackingGroup, trait: Trait): boolean {
    const filters = group.pairFilters;
    if (!filters) return false;
    const id = trait.id;
    for (let i = 0; i < filters.length; i++) {
        if (filters[i].trait.id === id) return true;
    }
    return false;
}

/**
 * Record a pair-level add, remove, or change for every tracking modifier,
 * then refresh queries that filter on this relation.
 */
function oppositeStillPending(
    world: World,
    trackingId: number,
    eid: number,
    traitId: number,
    kind: PairEventKind,
    target: number
): boolean {
    const opposite: PairEventKind | null = kind === 'add' ? 'remove' : kind === 'remove' ? 'add' : null;
    if (!opposite) return false;

    let sawGroup = false;
    for (const query of world[$internal].queriesHashMap.values()) {
        const groups = query.trackingGroups;
        for (let i = 0; i < groups.length; i++) {
            const group = groups[i];
            if (group.id !== trackingId || !group.pairFilters) continue;
            let tracks = false;
            for (let j = 0; j < group.pairFilters.length; j++) {
                if (group.pairFilters[j].trait.id === traitId) {
                    tracks = true;
                    break;
                }
            }
            if (!tracks) continue;
            sawGroup = true;
            const bucket = group.pairState?.get(eid)?.get(traitId);
            const set = bucket ? bucket[opposite] : undefined;
            if (set?.has(target)) return true;
        }
    }

    // No query has observed this modifier yet, so the whole history is one window.
    return !sawGroup;
}

function applyGlobalEvent(
    bucket: PairBucket,
    kind: PairEventKind,
    target: number,
    oppositePending: boolean
): void {
    if (kind === 'add' && bucket.remove.has(target)) {
        bucket.remove.delete(target);
        if (!oppositePending) bucket.add.add(target);
        return;
    }

    if (kind === 'remove' && bucket.add.has(target)) {
        bucket.add.delete(target);
        bucket.change.delete(target);
        if (!oppositePending) bucket.remove.add(target);
        return;
    }

    applyPairEvent(bucket, kind, target);
}

export function recordPairEvent(
    world: World,
    entity: Entity,
    trait: Trait,
    target: Entity,
    kind: PairEventKind,
    force = false
): void {
    const ctx = world[$internal];
    const eid = getEntityId(entity);
    const traitId = trait.id;

    for (const trackingId of ctx.dirtyMasks.keys()) {
        const bucket = bucketFor(globalStateFor(world, trackingId), eid, traitId);
        if (force && kind === 'remove') {
            bucket.add.delete(target);
            bucket.change.delete(target);
            bucket.remove.add(target);
            continue;
        }
        const oppositePending = oppositeStillPending(world, trackingId, eid, traitId, kind, target);
        applyGlobalEvent(bucket, kind, target, oppositePending);
    }

    const instance = getTraitInstance(ctx.traitInstances, trait);
    if (!instance) return;

    for (const query of instance.trackingQueries) {
        let touched = false;
        const groups = query.trackingGroups;
        for (let i = 0; i < groups.length; i++) {
            const group = groups[i];
            if (!groupTracksTrait(group, trait)) continue;
            if (!group.pairState) group.pairState = new Map();
            const local = bucketFor(group.pairState, eid, traitId);
            if (force && kind === 'remove') {
                local.add.delete(target);
                local.change.delete(target);
                local.remove.add(target);
            } else {
                applyPairEvent(local, kind, target);
            }
            touched = true;
        }
        if (!touched) continue;

        const match =
            query.relationFilters && query.relationFilters.length > 0
                ? checkQueryTrackingWithRelations(world, query, entity, 'add', 0, 0)
                : query.checkTracking(world, entity, 'add', 0, 0);
        if (match) query.add(entity);
        else query.remove(world, entity);
    }
}

export function pairFilterMatches(group: TrackingGroup, eid: number, filter: TrackedRelationPair): boolean {
    const bucket = group.pairState?.get(eid)?.get(filter.trait.id);
    if (!bucket) return false;

    const set = group.type === 'add' ? bucket.add : group.type === 'remove' ? bucket.remove : bucket.change;
    if (filter.target === '*') return set.size > 0;
    return set.has(filter.target as number);
}

/** True when this group's pair filters are satisfied for the entity. */
export function pairGroupMatches(group: TrackingGroup, eid: number): boolean {
    const filters = group.pairFilters;
    if (!filters || filters.length === 0) return true;

    if (group.logic === 'or') {
        for (let i = 0; i < filters.length; i++) {
            if (pairFilterMatches(group, eid, filters[i])) return true;
        }
        return false;
    }

    for (let i = 0; i < filters.length; i++) {
        if (!pairFilterMatches(group, eid, filters[i])) return false;
    }
    return true;
}

/**
 * First wildcard target currently pending on this query for a relation trait.
 * Used to resolve per-target store data while iterating results.
 */
export function resolveWildcardTarget(
    query: QueryInstance,
    eid: number,
    traitId: number
): number | undefined {
    const groups = query.trackingGroups;
    for (let i = 0; i < groups.length; i++) {
        const group = groups[i];
        const filters = group.pairFilters;
        if (!filters) continue;
        for (let j = 0; j < filters.length; j++) {
            const filter = filters[j];
            if (filter.trait.id !== traitId || filter.target !== '*') continue;
            const bucket = group.pairState?.get(eid)?.get(traitId);
            if (!bucket) continue;
            const set =
                group.type === 'add' ? bucket.add : group.type === 'remove' ? bucket.remove : bucket.change;
            const next = set.values().next();
            if (!next.done) return next.value;
        }
    }
    return undefined;
}

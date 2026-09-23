import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import type { Relation } from '../../relation/types';
import { getTraitInstance } from '../../trait/trait-instance';
import type { Trait } from '../../trait/types';
import type { World } from '../../world';
import type { EventType, PairEventRecord, PairTracker, QueryInstance, TrackingPair } from '../types';
import { checkQueryTrackingWithRelations } from './check-query-tracking-with-relations';
import { hasTrackingModifiers } from './tracking-cursor';

/**
 * Get or create the pair tracker for a tracking modifier and relation on a query.
 * The tracker is seeded with pair events that happened since the modifier started tracking.
 */
export function getPairTracker(
    world: World,
    query: QueryInstance,
    id: number,
    type: EventType,
    relationTrait: Trait
): PairTracker {
    for (const tracker of query.pairTrackers) {
        if (tracker.id === id && tracker.relationTrait === relationTrait) return tracker;
    }

    const ctx = world[$internal];
    const tracker: PairTracker = {
        id,
        type,
        relation: relationTrait[$internal].relation as Relation<Trait>,
        relationTrait,
        pending: new Map(),
    };

    const since = ctx.trackingPairSeqs.get(id) ?? 0;
    const events = ctx.pairEvents.get(relationTrait.id);

    if (events) {
        for (const [entity, records] of events) {
            let pending: Set<Entity> | undefined;
            for (const [target, record] of records) {
                if (!isPendingEvent(record, type, since)) continue;
                if (!pending) tracker.pending.set(entity, (pending = new Set()));
                pending.add(target);
            }
        }
    }

    query.pairTrackers.push(tracker);

    return tracker;
}

function isPendingEvent(
    record: PairEventRecord,
    type: EventType,
    since: number
): boolean {
    switch (type) {
        case 'add':
            return record.added > since && record.added > record.removed;
        case 'remove':
            return record.removed > since && record.removed > record.added;
        case 'change':
            return record.changed > since && record.changed > record.removed;
    }
}

/** Check whether a tracked pair has a pending event for the entity. */
export function checkTrackingPair(pair: TrackingPair, entity: Entity): boolean {
    const pending = pair.tracker.pending.get(entity);
    if (!pending) return false;
    return pair.target === '*' ? pending.size > 0 : pending.has(pair.target);
}

/** Start a new observation window for the entity on all pair trackers of the query. */
export function resetPairTrackers(query: QueryInstance, entity: Entity) {
    const trackers = query.pairTrackers;
    for (let i = 0; i < trackers.length; i++) {
        trackers[i].pending.delete(entity);
    }
}

/** Drop the recorded pair events of an entity whose ID is about to be recycled. */
export function forgetPairEvents(world: World, entity: Entity) {
    const ctx = world[$internal];
    if (ctx.pairEvents.size === 0) return;
    for (const events of ctx.pairEvents.values()) {
        events.delete(entity);
    }
}

/**
 * Record a pair-level event and update the tracking queries that track pairs of the relation.
 */
export function notifyPairEvent(
    world: World,
    entity: Entity,
    relationTrait: Trait,
    target: Entity,
    type: EventType
) {
    const ctx = world[$internal];

    // Record the event so queries created later can be seeded with it.
    if (hasTrackingModifiers()) {
        let events = ctx.pairEvents.get(relationTrait.id);
        if (!events) ctx.pairEvents.set(relationTrait.id, (events = new Map()));
        let records = events.get(entity);
        if (!records) events.set(entity, (records = new Map()));
        let record = records.get(target);
        if (!record) records.set(target, (record = { added: 0, removed: 0, changed: 0 }));

        const seq = ++ctx.pairEventSeq;
        if (type === 'add') record.added = seq;
        else if (type === 'remove') record.removed = seq;
        else record.changed = seq;
    }

    const instance = getTraitInstance(ctx.traitInstances, relationTrait);
    if (!instance || instance.pairTrackingQueries.size === 0) return;

    for (const query of instance.pairTrackingQueries) {
        const trackers = query.pairTrackers;
        for (let i = 0; i < trackers.length; i++) {
            const tracker = trackers[i];
            if (tracker.relationTrait !== relationTrait) continue;

            if (tracker.type === type) {
                let pending = tracker.pending.get(entity);
                if (!pending) tracker.pending.set(entity, (pending = new Set()));
                pending.add(target);
            } else if (cancelsPendingEvent(type, tracker.type)) {
                const pending = tracker.pending.get(entity);
                if (pending) {
                    pending.delete(target);
                    if (pending.size === 0) tracker.pending.delete(entity);
                }
            }
        }

        // No trait bitflag is involved, so the trait-level trackers are left untouched.
        const match = checkQueryTrackingWithRelations(world, query, entity, type, -1, 0);
        if (match) {
            if (!query.entities.has(entity) || query.toRemove.has(entity)) query.add(entity);
        } else {
            query.remove(world, entity);
        }
    }
}

function cancelsPendingEvent(event: EventType, pending: EventType): boolean {
    // A removal cancels a pending addition or change, an addition cancels a pending removal.
    return event === 'remove' ? pending !== 'remove' : event === 'add' && pending === 'remove';
}

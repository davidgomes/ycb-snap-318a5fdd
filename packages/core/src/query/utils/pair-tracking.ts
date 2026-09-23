import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { isEntityAlive } from '../../entity/utils/entity-index';
import { getEntityId } from '../../entity/utils/pack-entity';
import type { Relation } from '../../relation/types';
import { getTraitInstance } from '../../trait/trait-instance';
import type { Trait } from '../../trait/types';
import type { World } from '../../world';
import type { EventType, PairTracker, QueryInstance, TrackingPair } from '../types';
import { checkQueryTrackingWithRelations } from './check-query-tracking-with-relations';

const EMPTY_TARGETS: readonly Entity[] = Object.freeze([]);

/**
 * Get the current targets of a relation on an entity without allocating.
 * The returned array must not be mutated or retained.
 */
export function getCurrentPairTargets(
    world: World,
    relationTrait: Trait,
    entity: Entity
): readonly Entity[] {
    const ctx = world[$internal];
    const instance = getTraitInstance(ctx.traitInstances, relationTrait);
    if (!instance || !instance.relationTargets) return EMPTY_TARGETS;
    if (!isEntityAlive(ctx.entityIndex, entity)) return EMPTY_TARGETS;

    const eid = getEntityId(entity);

    if (relationTrait[$internal].relation![$internal].exclusive) {
        const target = (instance.relationTargets as Array<Entity | undefined>)[eid];
        return target === undefined ? EMPTY_TARGETS : [target];
    }

    return (instance.relationTargets as Entity[][])[eid] ?? EMPTY_TARGETS;
}

/**
 * Snapshot the targets of every relation for all alive entities.
 * Keyed by relation trait ID, then by source entity.
 */
export function snapshotPairTargets(world: World): Map<number, Map<Entity, readonly Entity[]>> {
    const ctx = world[$internal];
    const { dense, sparse, aliveCount } = ctx.entityIndex;
    const snapshot = new Map<number, Map<Entity, readonly Entity[]>>();

    for (const relation of ctx.relations) {
        const relationCtx = relation[$internal];
        const instance = getTraitInstance(ctx.traitInstances, relationCtx.trait);
        const relationTargets = instance?.relationTargets;
        if (!relationTargets) continue;

        const targets = new Map<Entity, readonly Entity[]>();

        for (let eid = 0; eid < relationTargets.length; eid++) {
            const value = relationTargets[eid] as Entity | Entity[] | undefined;
            if (value === undefined) continue;
            if (!relationCtx.exclusive && (value as Entity[]).length === 0) continue;

            const denseIndex = sparse[eid];
            if (denseIndex === undefined || denseIndex >= aliveCount) continue;

            const entity = dense[denseIndex];
            targets.set(entity, relationCtx.exclusive ? [value as Entity] : (value as Entity[]).slice());
        }

        if (targets.size > 0) snapshot.set(relationCtx.trait.id, targets);
    }

    return snapshot;
}

/**
 * Get or create the pair tracker for a tracking modifier and relation on a query.
 * The tracker is seeded from the world state recorded when the modifier was created.
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
        baseline: new Map(),
        changed: new Map(),
    };

    if (type === 'change') {
        const log = ctx.changedPairs.get(id)?.get(relationTrait.id);
        if (log) {
            for (const [entity, targets] of log) {
                if (targets.size > 0) tracker.changed.set(entity, new Set(targets));
            }
        }
    } else {
        const snapshot = ctx.trackingPairSnapshots.get(id)?.get(relationTrait.id);
        if (snapshot) tracker.baseline = new Map(snapshot);
    }

    query.pairTrackers.push(tracker);

    return tracker;
}

/**
 * Check whether a tracked pair has an event for the entity in the current observation window.
 */
export function checkTrackingPair(world: World, pair: TrackingPair, entity: Entity): boolean {
    const { tracker, target } = pair;
    const current = getCurrentPairTargets(world, tracker.relationTrait, entity);

    if (tracker.type === 'change') {
        const changed = tracker.changed.get(entity);
        if (!changed) return false;
        if (target !== '*') return changed.has(target) && current.includes(target);
        for (const t of changed) {
            if (current.includes(t)) return true;
        }
        return false;
    }

    const baseline = tracker.baseline.get(entity) ?? EMPTY_TARGETS;
    // Added looks for targets present now but not in the baseline, Removed the reverse.
    return tracker.type === 'add'
        ? hasTargetNotIn(current, baseline, target)
        : hasTargetNotIn(baseline, current, target);
}

function hasTargetNotIn(
    targets: readonly Entity[],
    exclude: readonly Entity[],
    target: Entity | '*'
): boolean {
    if (target !== '*') return targets.includes(target) && !exclude.includes(target);
    for (let i = 0; i < targets.length; i++) {
        if (!exclude.includes(targets[i])) return true;
    }
    return false;
}

/** Start a new observation window for the entity on all pair trackers of the query. */
export function resetPairTrackers(world: World, query: QueryInstance, entity: Entity) {
    const trackers = query.pairTrackers;
    for (let i = 0; i < trackers.length; i++) {
        const tracker = trackers[i];
        if (tracker.type === 'change') {
            tracker.changed.delete(entity);
            continue;
        }
        const current = getCurrentPairTargets(world, tracker.relationTrait, entity);
        if (current.length > 0) tracker.baseline.set(entity, current.slice());
        else tracker.baseline.delete(entity);
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
    const traitId = relationTrait.id;

    // World-level changed log, used to seed queries created after the change happened.
    if (type === 'change') {
        for (const log of ctx.changedPairs.values()) {
            let byEntity = log.get(traitId);
            if (!byEntity) log.set(traitId, (byEntity = new Map()));
            let targets = byEntity.get(entity);
            if (!targets) byEntity.set(entity, (targets = new Set()));
            targets.add(target);
        }
    } else if (type === 'remove') {
        for (const log of ctx.changedPairs.values()) {
            log.get(traitId)?.get(entity)?.delete(target);
        }
    }

    const instance = getTraitInstance(ctx.traitInstances, relationTrait);
    if (!instance || instance.pairTrackingQueries.size === 0) return;

    for (const query of instance.pairTrackingQueries) {
        if (type !== 'add') {
            const trackers = query.pairTrackers;
            for (let i = 0; i < trackers.length; i++) {
                const tracker = trackers[i];
                if (tracker.type !== 'change' || tracker.relationTrait !== relationTrait) continue;
                if (type === 'change') {
                    let targets = tracker.changed.get(entity);
                    if (!targets) tracker.changed.set(entity, (targets = new Set()));
                    targets.add(target);
                } else {
                    tracker.changed.get(entity)?.delete(target);
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

import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import type { Relation } from '../../relation/types';
import { getTraitInstance } from '../../trait/trait-instance';
import type { Trait } from '../../trait/types';
import type { World } from '../../world';
import type {
    EventType,
    PairFilter,
    PairNetRecord,
    PairNetState,
    PairSnapshot,
    QueryInstance,
} from '../types';
import { checkQueryTracking, hasTrackingOrGroup, trackingOrMatches } from './check-query-tracking';

export function pairNetKey(trackingId: number, entity: Entity, relationTraitId: number, target: Entity) {
    return `${trackingId}:${entity}:${relationTraitId}:${target}`;
}

function globalNetKey(entity: Entity, relationTraitId: number, target: Entity) {
    return `${entity}:${relationTraitId}:${target}`;
}

export function transitionPairNet(
    current: PairNetState | undefined,
    event: EventType
): PairNetState | undefined {
    if (event === 'add') {
        if (current === 'remove') return undefined;
        if (current === 'change') return 'add-change';
        if (current === 'add' || current === 'add-change') return current;
        return 'add';
    }

    if (event === 'remove') {
        if (current === 'add' || current === 'add-change') return undefined;
        return 'remove';
    }

    if (current === 'remove') return 'remove';
    if (current === 'add' || current === 'add-change') return 'add-change';
    return 'change';
}

export function netMatches(net: PairNetState | undefined, kind: EventType): boolean {
    if (!net) return false;
    if (kind === 'add') return net === 'add' || net === 'add-change';
    if (kind === 'remove') return net === 'remove';
    return net === 'change' || net === 'add-change';
}

function writeNet(
    nets: Map<string, PairNetRecord>,
    key: string,
    event: EventType,
    base: { entity: Entity; relationTraitId: number; target: Entity; removedData?: unknown },
    forceRemove = false
) {
    if (forceRemove) {
        nets.set(key, {
            net: 'remove',
            entity: base.entity,
            relationTraitId: base.relationTraitId,
            target: base.target,
            removedData: base.removedData,
        });
        return;
    }

    const prev = nets.get(key);
    const next = transitionPairNet(prev?.net, event);
    if (!next) {
        nets.delete(key);
        return;
    }

    nets.set(key, {
        net: next,
        entity: base.entity,
        relationTraitId: base.relationTraitId,
        target: base.target,
        removedData: event === 'remove' ? base.removedData : event === 'add' ? undefined : prev?.removedData,
    });
}

function queriesForRelation(world: World, trait: Trait) {
    return getTraitInstance(world[$internal].traitInstances, trait)?.pairQueries;
}

type PairRecheck = (world: World, query: QueryInstance, entity: Entity) => boolean;

function defaultPairRecheck(world: World, query: QueryInstance, entity: Entity) {
    const pairs = evaluatePairFilters(query, entity);
    const traitOk = checkQueryTracking(world, query, entity, 'add', 0, 0, pairs.hasOr);
    if (!traitOk || !pairs.andOk) return false;

    if (pairs.hasOr || hasTrackingOrGroup(query)) {
        const traitOr = hasTrackingOrGroup(query) ? trackingOrMatches(query, entity) : false;
        if (pairs.hasOr) {
            if (!traitOr && !pairs.orMatch) return false;
        } else if (!traitOr) {
            return false;
        }
    }

    return true;
}

let recheckPairQuery: PairRecheck = defaultPairRecheck;

/** Install the full matcher (relation filters included) once query checks are loaded. */
export function setPairQueryRecheck(fn: PairRecheck) {
    recheckPairQuery = fn;
}

/**
 * Drop an unobserved add so the following removal stands on its own.
 * Exclusive replacement uses this so the old target is still reported as removed.
 */
export function dropPendingPairAdd(
    world: World,
    entity: Entity,
    relation: Relation<Trait>,
    target: Entity
) {
    const trait = relation[$internal].trait;
    const relationTraitId = trait.id;
    const globalKey = globalNetKey(entity, relationTraitId, target);

    for (const tracker of world[$internal].pairTrackers.values()) {
        const rec = tracker.nets.get(globalKey);
        if (rec && (rec.net === 'add' || rec.net === 'add-change')) tracker.nets.delete(globalKey);
    }

    for (const query of queriesForRelation(world, trait) ?? []) {
        for (const filter of query.pairFilters) {
            if (filter.relationTraitId !== relationTraitId) continue;
            const key = pairNetKey(filter.id, entity, relationTraitId, target);
            const rec = query.pairNets.get(key);
            if (rec && (rec.net === 'add' || rec.net === 'add-change')) query.pairNets.delete(key);
        }
    }
}

export function recordPairEvent(
    world: World,
    entity: Entity,
    relation: Relation<Trait>,
    target: Entity,
    event: EventType,
    removedData?: unknown
) {
    const trait = relation[$internal].trait;
    const relationTraitId = trait.id;
    const base = { entity, relationTraitId, target, removedData };
    const globalKey = globalNetKey(entity, relationTraitId, target);
    const queries = queriesForRelation(world, trait);
    const forceRemove = event === 'remove' && world[$internal].forcingPairRemovals;

    for (const [trackingId, tracker] of world[$internal].pairTrackers) {
        writeNet(tracker.nets, globalKey, event, base, forceRemove);
        if (!queries) continue;

        const localKey = pairNetKey(trackingId, entity, relationTraitId, target);
        for (const query of queries) {
            if (!query.pairFilters.some((filter) => filter.id === trackingId)) continue;
            writeNet(query.pairNets, localKey, event, base, forceRemove);
        }
    }

    if (!queries) return;
    for (const query of queries) {
        if (recheckPairQuery(world, query, entity)) query.add(entity);
        else query.remove(world, entity);
    }
}

export function seedPairNets(world: World, query: QueryInstance) {
    const trackers = world[$internal].pairTrackers;

    for (const filter of query.pairFilters) {
        const tracker = trackers.get(filter.id);
        if (!tracker) continue;

        for (const rec of tracker.nets.values()) {
            if (rec.relationTraitId !== filter.relationTraitId) continue;
            if (filter.target !== '*' && filter.target !== rec.target) continue;
            const key = pairNetKey(filter.id, rec.entity, rec.relationTraitId, rec.target);
            query.pairNets.set(key, { ...rec });
        }
    }
}

export function evaluatePairFilters(query: QueryInstance, entity: Entity) {
    let andOk = true;
    let orMatch = false;
    let hasOr = false;
    for (const filter of query.pairFilters) {
        const matched = filterMatches(query, filter, entity);
        if (filter.logic === 'or') {
            hasOr = true;
            if (matched) orMatch = true;
        } else if (!matched) {
            andOk = false;
        }
    }

    return { andOk, orMatch, hasOr };
}

function filterMatches(query: QueryInstance, filter: PairFilter, entity: Entity) {
    if (filter.target === '*') {
        const prefix = `${filter.id}:${entity}:${filter.relationTraitId}:`;
        for (const [key, rec] of query.pairNets) {
            if (rec.entity !== entity) continue;
            if (!key.startsWith(prefix)) continue;
            if (netMatches(rec.net, filter.type)) return true;
        }
        return false;
    }

    const key = pairNetKey(filter.id, entity, filter.relationTraitId, filter.target);
    return netMatches(query.pairNets.get(key)?.net, filter.type);
}

export function buildPairSnapshots(query: QueryInstance, entities: readonly Entity[]) {
    const snaps = new Map<string, PairSnapshot>();

    for (const entity of entities) {
        for (const filter of query.pairFilters) {
            if (filter.target === '*') {
                const prefix = `${filter.id}:${entity}:${filter.relationTraitId}:`;
                let chosen: PairNetRecord | undefined;
                for (const [key, rec] of query.pairNets) {
                    if (rec.entity !== entity) continue;
                    if (!key.startsWith(prefix)) continue;
                    if (!netMatches(rec.net, filter.type)) continue;
                    chosen = rec;
                }
                if (!chosen) continue;
                snaps.set(snapshotKey(entity, filter.id, filter.relationTraitId, '*'), {
                    target: chosen.target,
                    removedData: chosen.removedData,
                    removed: chosen.net === 'remove',
                });
                continue;
            }

            const rec = query.pairNets.get(
                pairNetKey(filter.id, entity, filter.relationTraitId, filter.target)
            );
            if (!rec || !netMatches(rec.net, filter.type)) continue;
            snaps.set(snapshotKey(entity, filter.id, filter.relationTraitId, filter.target), {
                target: filter.target,
                removedData: rec.removedData,
                removed: rec.net === 'remove',
            });
        }
    }

    query.pairSnapshots = snaps;
}

export function snapshotKey(
    entity: Entity,
    trackingId: number,
    relationTraitId: number,
    target: Entity | '*'
) {
    return `${entity}:${trackingId}:${relationTraitId}:${target}`;
}

/**
 * Close this query's observation window.
 * Returned pair events are consumed. Pending events of the wrong kind are dropped
 * so a later opposite event is not cancelled by one that was already observed.
 * Matching events for entities excluded by other constraints are kept.
 */
export function closePairWindow(query: QueryInstance, returned: readonly Entity[]) {
    if (!query.pairFilters.length) return;

    const returnedSet = new Set<Entity>(returned);

    for (const [key, rec] of query.pairNets) {
        let matchesFilter = false;
        for (const filter of query.pairFilters) {
            if (!key.startsWith(`${filter.id}:`)) continue;
            if (rec.relationTraitId !== filter.relationTraitId) continue;
            if (filter.target !== '*' && filter.target !== rec.target) continue;
            if (!netMatches(rec.net, filter.type)) continue;
            matchesFilter = true;
            break;
        }

        if (!matchesFilter || returnedSet.has(rec.entity)) query.pairNets.delete(key);
    }
}

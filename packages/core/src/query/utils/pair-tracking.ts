import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import { hasRelationPair } from '../../relation/relation';
import type { Trait } from '../../trait/types';
import { getTraitInstance } from '../../trait/trait-instance';
import type { World } from '../../world';
import type { EventType, PairFilter, PairSlot, QueryInstance, TrackingGroup } from '../types';

export function slotMatches(slot: PairSlot, type: EventType): boolean {
    const net = slot.adds - slot.removes;
    if (type === 'add') return slot.pendingAdd || net > 0;
    if (type === 'remove') return slot.pendingRemove || net < 0;
    if (net < 0 || slot.pendingRemove) return false;
    if (net === 0 && slot.removes > 0) return false;
    return slot.pendingChange || slot.changes > 0;
}

function slotIsEmpty(slot: PairSlot): boolean {
    if (slot.pendingAdd || slot.pendingRemove || slot.pendingChange) return false;
    const net = slot.adds - slot.removes;
    if (net !== 0) return false;
    if (slot.removes > 0) return true;
    return slot.changes === 0;
}

function findSlot(group: TrackingGroup, entity: Entity, traitId: number, target: Entity): PairSlot {
    let slots = group.pairSlots.get(entity);
    if (!slots) {
        slots = [];
        group.pairSlots.set(entity, slots);
    }

    for (let i = 0; i < slots.length; i++) {
        const candidate = slots[i];
        if (candidate.traitId === traitId && candidate.target === target) return candidate;
    }

    const slot: PairSlot = {
        traitId,
        target,
        pendingAdd: false,
        pendingRemove: false,
        pendingChange: false,
        adds: 0,
        removes: 0,
        changes: 0,
    };
    slots.push(slot);
    return slot;
}

function dropIfEmpty(group: TrackingGroup, entity: Entity, slot: PairSlot): void {
    if (!slotIsEmpty(slot)) return;
    const slots = group.pairSlots.get(entity);
    if (!slots) return;
    const index = slots.indexOf(slot);
    if (index !== -1) slots.splice(index, 1);
    if (slots.length === 0) group.pairSlots.delete(entity);
}

/** Replay path: fold events with symmetric add/remove cancellation. */
export function applyPairSlot(
    group: TrackingGroup,
    entity: Entity,
    traitId: number,
    target: Entity,
    type: EventType
): void {
    const slot = findSlot(group, entity, traitId, target);
    if (type === 'add') slot.adds++;
    else if (type === 'remove') slot.removes++;
    else slot.changes++;
    dropIfEmpty(group, entity, slot);
}

/**
 * Live path. History frozen into pending flags is not cancelled by new events of the
 * other kind on Removed/Added groups that did not record that history as pending.
 * An unobserved pending add is cleared by a later remove, and the reverse.
 */
function applyLivePairSlot(
    group: TrackingGroup,
    entity: Entity,
    traitId: number,
    target: Entity,
    type: EventType
): void {
    const slot = findSlot(group, entity, traitId, target);

    if (type === 'add') {
        if (slot.pendingRemove) slot.pendingRemove = false;
        else if (slot.removes > slot.adds) slot.removes--;
        else slot.adds++;
    } else if (type === 'remove') {
        if (slot.pendingAdd) slot.pendingAdd = false;
        else if (slot.adds > slot.removes) slot.adds--;
        else slot.removes++;
        slot.pendingChange = false;
        slot.changes = 0;
    } else if (!(slot.pendingRemove || slot.removes > slot.adds)) {
        slot.changes++;
    }

    dropIfEmpty(group, entity, slot);
}

/** Turn replayed counters into the query's starting observation. */
export function commitPairHistory(group: TrackingGroup): void {
    for (const [entity, slots] of group.pairSlots) {
        for (let i = slots.length - 1; i >= 0; i--) {
            const slot = slots[i];
            const net = slot.adds - slot.removes;
            if (group.type === 'add') slot.pendingAdd = net > 0;
            else if (group.type === 'remove') slot.pendingRemove = net < 0;
            else slot.pendingChange = slot.changes > 0 && net >= 0 && !(net === 0 && slot.removes > 0);
            slot.adds = 0;
            slot.removes = 0;
            slot.changes = 0;
            if (slotIsEmpty(slot)) slots.splice(i, 1);
        }
        if (slots.length === 0) group.pairSlots.delete(entity);
    }
}

function pairFilterMatches(group: TrackingGroup, entity: Entity, pair: PairFilter): boolean {
    const slots = group.pairSlots.get(entity);
    if (!slots) return false;
    for (let i = 0; i < slots.length; i++) {
        const slot = slots[i];
        if (slot.traitId !== pair.traitId) continue;
        if (pair.target !== '*' && slot.target !== pair.target) continue;
        if (slotMatches(slot, group.type)) return true;
    }
    return false;
}

/**
 * Whether this group's pair filters match.
 * Returns null when the group does not track pairs.
 */
export function pairGroupSatisfied(group: TrackingGroup, entity: Entity): boolean | null {
    const pairs = group.pairs;
    if (!pairs || pairs.length === 0) return null;

    if (group.logic === 'or') {
        for (let i = 0; i < pairs.length; i++) {
            if (pairFilterMatches(group, entity, pairs[i])) return true;
        }
        return false;
    }

    for (let i = 0; i < pairs.length; i++) {
        if (!pairFilterMatches(group, entity, pairs[i])) return false;
    }
    return true;
}

export function queryTracksPairs(query: QueryInstance): boolean {
    const groups = query.trackingGroups;
    for (let i = 0; i < groups.length; i++) {
        if (groups[i].pairs.length > 0) return true;
    }
    return false;
}

function reevaluate(world: World, query: QueryInstance, entity: Entity): void {
    if (!query.checkTracking(world, entity, 'change', 0, 0)) {
        query.remove(world, entity);
        return;
    }

    const filters = query.relationFilters;
    if (filters && filters.length > 0) {
        for (let i = 0; i < filters.length; i++) {
            if (!hasRelationPair(world, entity, filters[i])) {
                query.remove(world, entity);
                return;
            }
        }
    }

    query.add(entity);
}

/**
 * Record a relation pair add, remove, or change and update live tracking queries.
 * Add and remove of the same target cancel inside one observation window.
 */
export function recordPairEvent(
    world: World,
    entity: Entity,
    trait: Trait,
    target: Entity,
    type: EventType
): void {
    const ctx = world[$internal];
    ctx.pairEvents.push({ entity, traitId: trait.id, target, type });

    const instance = getTraitInstance(ctx.traitInstances, trait);
    if (!instance) return;

    for (const query of instance.trackingQueries) {
        let touched = false;
        const groups = query.trackingGroups;
        for (let i = 0; i < groups.length; i++) {
            const group = groups[i];
            if (group.pairs.length === 0) continue;

            let relevant = false;
            for (let p = 0; p < group.pairs.length; p++) {
                if (group.pairs[p].traitId === trait.id) {
                    relevant = true;
                    break;
                }
            }
            if (!relevant) continue;

            applyLivePairSlot(group, entity, trait.id, target, type);
            touched = true;
        }

        if (touched) reevaluate(world, query, entity);
    }
}

/** Fold historical pair events into a newly created query. */
export function replayPairHistory(world: World, query: QueryInstance): void {
    const ctx = world[$internal];
    const events = ctx.pairEvents;
    if (events.length === 0) return;

    const groups = query.trackingGroups;
    for (let g = 0; g < groups.length; g++) {
        const group = groups[g];
        if (group.pairs.length === 0) continue;

        const cursor = ctx.pairEventCursors.get(group.id) ?? 0;
        const traitIds = new Set<number>();
        for (let p = 0; p < group.pairs.length; p++) traitIds.add(group.pairs[p].traitId);

        for (let i = cursor; i < events.length; i++) {
            const event = events[i];
            if (!traitIds.has(event.traitId)) continue;
            applyPairSlot(group, event.entity, event.traitId, event.target, event.type);
        }
        commitPairHistory(group);
    }
}

function initialBitsMatch(world: World, group: TrackingGroup, entity: Entity): boolean {
    const bitmasks = group.bitmasks;
    let hasBits = false;
    for (let i = 0; i < bitmasks.length; i++) {
        if (bitmasks[i]) {
            hasBits = true;
            break;
        }
    }
    if (!hasBits) return group.logic === 'and';

    const ctx = world[$internal];
    const snapshot = ctx.trackingSnapshots.get(group.id);
    const dirtyMask = ctx.dirtyMasks.get(group.id);
    const changedMask = ctx.changedMasks.get(group.id);
    if (!snapshot || !dirtyMask || !changedMask) return false;

    const eid = getEntityId(entity);

    for (let genId = 0; genId < bitmasks.length; genId++) {
        const mask = bitmasks[genId];
        if (!mask) continue;

        const oldMask = snapshot[genId]?.[eid] || 0;
        const currentMask = ctx.entityMasks[genId]?.[eid] || 0;

        for (let bit = 1; bit <= mask; bit <<= 1) {
            if ((mask & bit) === 0) continue;

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

    return group.logic === 'and';
}

export function initialGroupMatches(world: World, group: TrackingGroup, entity: Entity): boolean {
    const bits = initialBitsMatch(world, group, entity);
    const pairs = pairGroupSatisfied(group, entity);

    if (group.logic === 'and') {
        if (!bits) return false;
        if (pairs === false) return false;
        return true;
    }

    if (bits) return true;
    return pairs === true;
}

function queryHasLiveConstraints(query: QueryInstance): boolean {
    if (query.relationFilters && query.relationFilters.length > 0) return true;
    const masks = query.staticBitmasks;
    for (let i = 0; i < masks.length; i++) {
        const mask = masks[i];
        if (mask && (mask.required || mask.or)) return true;
    }
    return false;
}

/**
 * Populate a tracking query that includes relation pairs.
 * Trait bitmask history and pair history must both hold, along with static filters.
 */
export function populatePairTrackingQuery(world: World, query: QueryInstance): void {
    const ctx = world[$internal];
    const hasLiveConstraints = queryHasLiveConstraints(query);
    const seen = new Set<Entity>();

    const consider = (entity: Entity, alive: boolean) => {
        if (seen.has(entity)) return;
        seen.add(entity);

        if (alive) {
            if (!query.check(world, entity)) return;
        } else if (hasLiveConstraints) {
            return;
        }

        let hasOr = false;
        let anyOr = false;
        const groups = query.trackingGroups;
        for (let i = 0; i < groups.length; i++) {
            const group = groups[i];
            const ok = initialGroupMatches(world, group, entity);
            if (group.logic === 'and') {
                if (!ok) return;
            } else {
                hasOr = true;
                if (ok) anyOr = true;
            }
        }
        if (hasOr && !anyOr) return;

        if (alive && query.relationFilters && query.relationFilters.length > 0) {
            for (let i = 0; i < query.relationFilters.length; i++) {
                if (!hasRelationPair(world, entity, query.relationFilters[i])) return;
            }
        }

        query.add(entity);
    };

    const entities = ctx.entityIndex.dense;
    for (let i = 0; i < entities.length; i++) consider(entities[i], true);

    // Destroyed entities are no longer in the index, but pair removals still count.
    const events = ctx.pairEvents;
    for (let i = 0; i < events.length; i++) {
        const entity = events[i].entity;
        if (world.has(entity)) continue;
        consider(entity, false);
    }
}

/** Last target that satisfies a wildcard pair filter, keyed by entity then trait id. */
export function captureWildcardTargets(query: QueryInstance, entities: readonly Entity[]): Map<
    Entity,
    Map<number, Entity>
> | null {
    let anyWildcard = false;
    const groups = query.trackingGroups;
    for (let i = 0; i < groups.length; i++) {
        const pairs = groups[i].pairs;
        for (let p = 0; p < pairs.length; p++) {
            if (pairs[p].target === '*') {
                anyWildcard = true;
                break;
            }
        }
        if (anyWildcard) break;
    }
    if (!anyWildcard) return null;

    const result = new Map<Entity, Map<number, Entity>>();
    for (let e = 0; e < entities.length; e++) {
        const entity = entities[e];
        const byTrait = new Map<number, Entity>();
        for (let g = 0; g < groups.length; g++) {
            const group = groups[g];
            const slots = group.pairSlots.get(entity);
            if (!slots) continue;
            for (let p = 0; p < group.pairs.length; p++) {
                const pair = group.pairs[p];
                if (pair.target !== '*') continue;
                for (let s = 0; s < slots.length; s++) {
                    const slot = slots[s];
                    if (slot.traitId !== pair.traitId) continue;
                    if (!slotMatches(slot, group.type)) continue;
                    byTrait.set(pair.traitId, slot.target);
                }
            }
        }
        if (byTrait.size > 0) result.set(entity, byTrait);
    }

    return result;
}

export function clearPairSlots(query: QueryInstance, entity: Entity): void {
    const groups = query.trackingGroups;
    for (let i = 0; i < groups.length; i++) {
        groups[i].pairSlots.delete(entity);
    }
}

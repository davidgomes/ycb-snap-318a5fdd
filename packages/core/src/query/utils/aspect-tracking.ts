import type { TrackingGroup } from '../types';

/** True when every generation mask is fully present on the entity. */
export function masksComplete(
    masksSource: number[][],
    eid: number,
    bitmasks: (number | undefined)[]
): boolean {
    let any = false;
    for (let gen = 0; gen < bitmasks.length; gen++) {
        const mask = bitmasks[gen];
        if (!mask) continue;
        any = true;
        const current = masksSource[gen]?.[eid] || 0;
        if ((current & mask) !== mask) return false;
    }
    return any;
}

function masksCompleteWithBit(
    entityMasks: number[][],
    eid: number,
    bitmasks: (number | undefined)[],
    extraGen: number,
    extraBit: number
): boolean {
    let any = false;
    for (let gen = 0; gen < bitmasks.length; gen++) {
        const mask = bitmasks[gen];
        if (!mask) continue;
        any = true;
        let current = entityMasks[gen]?.[eid] || 0;
        if (gen === extraGen) current |= extraBit;
        if ((current & mask) !== mask) return false;
    }
    return any;
}

function ensureTracker(group: TrackingGroup, gen: number): number[] {
    let tracker = group.trackers[gen];
    if (!tracker) {
        tracker = [];
        group.trackers[gen] = tracker;
    }
    return tracker;
}

function anyTrackerBit(group: TrackingGroup, eid: number): boolean {
    const bitmasks = group.bitmasks;
    for (let gen = 0; gen < bitmasks.length; gen++) {
        const mask = bitmasks[gen];
        if (!mask) continue;
        const tracker = group.trackers[gen];
        if (tracker && (tracker[eid] & mask) !== 0) return true;
    }
    return false;
}

function anyPresentTrackerBit(group: TrackingGroup, eid: number, entityMasks: number[][]): boolean {
    const bitmasks = group.bitmasks;
    for (let gen = 0; gen < bitmasks.length; gen++) {
        const mask = bitmasks[gen];
        if (!mask) continue;
        const tracker = group.trackers[gen];
        if (!tracker) continue;
        const current = entityMasks[gen]?.[eid] || 0;
        if ((tracker[eid] & mask & current) !== 0) return true;
    }
    return false;
}

/**
 * Update aspect tracking state for one structural or change event.
 * The entity mask already reflects the event.
 */
export function applyAspectEvent(
    group: TrackingGroup,
    eid: number,
    entityMasks: number[][],
    eventType: 'add' | 'remove' | 'change',
    eventGen: number,
    eventBit: number
): void {
    const mask = group.bitmasks[eventGen];
    const intersects = !!mask && (mask & eventBit) !== 0;

    if (group.aspect === 'add') {
        if (intersects && eventType === 'add') {
            const tracker = ensureTracker(group, eventGen);
            tracker[eid] = (tracker[eid] | 0) | eventBit;
        }
        return;
    }

    if (group.aspect === 'remove') {
        const qualified = group.qualified ?? (group.qualified = []);
        if (intersects && eventType === 'remove') {
            if (masksCompleteWithBit(entityMasks, eid, group.bitmasks, eventGen, eventBit)) {
                qualified[eid] = 1;
            }
        } else if (eventType === 'add' && masksComplete(entityMasks, eid, group.bitmasks)) {
            qualified[eid] = 0;
        }
        return;
    }

    if (group.aspect === 'change') {
        if (intersects && eventType === 'change') {
            const current = entityMasks[eventGen]?.[eid] || 0;
            if (
                (current & eventBit) !== 0 &&
                masksComplete(entityMasks, eid, group.bitmasks)
            ) {
                const tracker = ensureTracker(group, eventGen);
                tracker[eid] = (tracker[eid] | 0) | eventBit;
            }
        } else if (intersects && eventType === 'remove') {
            const tracker = group.trackers[eventGen];
            if (tracker) tracker[eid] = (tracker[eid] | 0) & ~eventBit;
        }
    }
}

/** Whether an aspect tracking group currently matches, based on tracker state. */
export function aspectGroupMatches(
    group: TrackingGroup,
    eid: number,
    entityMasks: number[][]
): boolean {
    if (group.aspect === 'add') {
        return masksComplete(entityMasks, eid, group.bitmasks) && anyTrackerBit(group, eid);
    }
    if (group.aspect === 'remove') {
        return (
            group.qualified?.[eid] === 1 && !masksComplete(entityMasks, eid, group.bitmasks)
        );
    }
    return masksComplete(entityMasks, eid, group.bitmasks) && anyPresentTrackerBit(group, eid, entityMasks);
}

/**
 * Seed tracker state from snapshots when a tracking query is first created.
 * Returns whether the entity matches the aspect group right now.
 */
export function seedAspectGroup(
    group: TrackingGroup,
    eid: number,
    entityMasks: number[][],
    snapshot: number[][],
    dirty: number[][],
    changed: number[][]
): boolean {
    if (group.aspect === 'add') {
        let anyAdded = false;
        const bitmasks = group.bitmasks;
        for (let gen = 0; gen < bitmasks.length; gen++) {
            const mask = bitmasks[gen];
            if (!mask) continue;
            const oldMask = snapshot[gen]?.[eid] || 0;
            const current = entityMasks[gen]?.[eid] || 0;
            const added = current & ~oldMask & mask;
            if (added) {
                anyAdded = true;
                ensureTracker(group, gen)[eid] = added;
            }
        }
        return masksComplete(entityMasks, eid, bitmasks) && anyAdded;
    }

    if (group.aspect === 'change') {
        let anyChanged = false;
        const bitmasks = group.bitmasks;
        for (let gen = 0; gen < bitmasks.length; gen++) {
            const mask = bitmasks[gen];
            if (!mask) continue;
            const current = entityMasks[gen]?.[eid] || 0;
            const bits = (changed[gen]?.[eid] || 0) & mask & current;
            if (bits) {
                anyChanged = true;
                ensureTracker(group, gen)[eid] = bits;
            }
        }
        return masksComplete(entityMasks, eid, bitmasks) && anyChanged;
    }

    const bitmasks = group.bitmasks;
    const hadAll = masksComplete(snapshot, eid, bitmasks);
    const hasNow = masksComplete(entityMasks, eid, bitmasks);
    let qualified = hadAll && !hasNow;

    if (!qualified && !hasNow) {
        let anyRemoved = false;
        let covered = true;
        let any = false;
        for (let gen = 0; gen < bitmasks.length; gen++) {
            const mask = bitmasks[gen];
            if (!mask) continue;
            any = true;
            const oldMask = snapshot[gen]?.[eid] || 0;
            const current = entityMasks[gen]?.[eid] || 0;
            const dirtyBits = dirty[gen]?.[eid] || 0;
            const removed = ~current & (oldMask | dirtyBits) & mask;
            if (removed) anyRemoved = true;
            if (((current | removed) & mask) !== mask) covered = false;
        }
        qualified = any && anyRemoved && covered;
    }

    const flags = group.qualified ?? (group.qualified = []);
    flags[eid] = qualified ? 1 : 0;
    return qualified && !hasNow;
}

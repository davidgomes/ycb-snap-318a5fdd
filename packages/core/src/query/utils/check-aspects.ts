import type { AspectBitmasks, QueryInstance, TrackingGroup } from '../types';

/** Whether the entity has every bit in the aspect's bitmasks. */
export function hasAllAspectBits(
    entityMasks: number[][],
    eid: number,
    bitmasks: AspectBitmasks
): boolean {
    for (let genId = 0; genId < bitmasks.length; genId++) {
        const mask = bitmasks[genId];
        if (!mask) continue;
        const genMasks = entityMasks[genId];
        const entityMask = genMasks ? genMasks[eid] | 0 : 0;
        if ((entityMask & mask) !== mask) return false;
    }
    return true;
}

/**
 * Check the aspect constraints of Not and Or modifiers.
 * Not(aspect) fails if the entity has every constituent.
 * Or(..., aspect) passes if any Or trait is present or any aspect is complete.
 */
export function checkAspectConstraints(
    query: QueryInstance,
    entityMasks: number[][],
    eid: number
): boolean {
    const notGroups = query.aspectNotGroups;
    for (let i = 0; i < notGroups.length; i++) {
        if (hasAllAspectBits(entityMasks, eid, notGroups[i])) return false;
    }

    const orGroups = query.aspectOrGroups;
    if (orGroups.length === 0) return true;

    const generations = query.generations;
    const staticBitmasks = query.staticBitmasks;
    for (let i = 0; i < generations.length; i++) {
        const or = staticBitmasks[i].or;
        if (!or) continue;
        const genMasks = entityMasks[generations[i]];
        const entityMask = genMasks ? genMasks[eid] | 0 : 0;
        if (entityMask & or) return true;
    }

    for (let i = 0; i < orGroups.length; i++) {
        if (hasAllAspectBits(entityMasks, eid, orGroups[i])) return true;
    }

    return false;
}

/**
 * Update the trackers of aspect groups affected by an event.
 * - Added: any constituent add is recorded. Satisfied while the aspect is complete.
 * - Removed: a constituent removal is recorded only if the aspect was complete before it.
 *   Completing the aspect again clears the record.
 * - Changed: any constituent change is recorded. Structural changes clear the record.
 */
export function updateAspectTrackers(
    group: TrackingGroup,
    entityMasks: number[][],
    eid: number,
    eventType: 'add' | 'remove' | 'change',
    eventGenerationId: number,
    eventBitflag: number
) {
    const bitmasks = group.bitmasks;
    const groupBitmask = bitmasks[eventGenerationId];
    if (!groupBitmask || !(groupBitmask & eventBitflag)) return;

    const type = group.type;
    let record = false;
    let clear = false;

    if (type === 'add') {
        record = eventType === 'add';
    } else if (type === 'remove') {
        if (eventType === 'remove') {
            record = wasCompleteBeforeRemoval(
                entityMasks,
                eid,
                bitmasks,
                eventGenerationId,
                eventBitflag
            );
        } else if (eventType === 'add') {
            clear = hasAllAspectBits(entityMasks, eid, bitmasks);
        }
    } else {
        record = eventType === 'change';
        clear = !record;
    }

    const trackers = group.trackers;

    if (record) {
        let trackerArr = trackers[eventGenerationId];
        if (!trackerArr) {
            trackerArr = [];
            trackers[eventGenerationId] = trackerArr;
        }
        trackerArr[eid] = trackerArr[eid] | 0 | eventBitflag;
    } else if (clear) {
        for (let genId = 0; genId < trackers.length; genId++) {
            const trackerArr = trackers[genId];
            if (trackerArr) trackerArr[eid] = 0;
        }
    }
}

export function isAspectGroupSatisfied(
    group: TrackingGroup,
    entityMasks: number[][],
    eid: number
): boolean {
    const bitmasks = group.bitmasks;
    const trackers = group.trackers;
    let tracked = false;

    for (let genId = 0; genId < bitmasks.length; genId++) {
        const mask = bitmasks[genId];
        if (!mask) continue;
        const trackerArr = trackers[genId];
        const tracker = trackerArr ? trackerArr[eid] | 0 : 0;
        if (tracker & mask) {
            tracked = true;
            break;
        }
    }

    if (!tracked) return false;

    const complete = hasAllAspectBits(entityMasks, eid, bitmasks);
    return group.type === 'remove' ? !complete : complete;
}

function wasCompleteBeforeRemoval(
    entityMasks: number[][],
    eid: number,
    bitmasks: AspectBitmasks,
    eventGenerationId: number,
    eventBitflag: number
): boolean {
    for (let genId = 0; genId < bitmasks.length; genId++) {
        const mask = bitmasks[genId];
        if (!mask) continue;
        const genMasks = entityMasks[genId];
        let entityMask = genMasks ? genMasks[eid] | 0 : 0;
        if (genId === eventGenerationId) entityMask |= eventBitflag;
        if ((entityMask & mask) !== mask) return false;
    }
    return true;
}

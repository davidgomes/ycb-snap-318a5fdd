import type { TraitInstance } from '../../trait/types';
import type { AspectConstraint, TrackingGroup } from '../types';

type Masks = (number[] | undefined)[];

/** Build bitmasks indexed by generationId from trait instances. */
export function createBitmasks(instances: TraitInstance[]): number[] {
    const bitmasks: number[] = [];
    for (let i = 0; i < instances.length; i++) {
        const { generationId, bitflag } = instances[i];
        bitmasks[generationId] = bitmasks[generationId] | 0 | bitflag;
    }
    return bitmasks;
}

/**
 * Whether an entity's masks contain every bit of the bitmasks.
 * An extra bit can be treated as set, which answers "was complete" during a remove event.
 */
export function hasAllBits(
    masks: Masks,
    eid: number,
    bitmasks: (number | undefined)[],
    extraGenerationId = -1,
    extraBitflag = 0
): boolean {
    for (let genId = 0; genId < bitmasks.length; genId++) {
        const bitmask = bitmasks[genId];
        if (!bitmask) continue;
        const genMasks = masks[genId];
        let mask = genMasks ? genMasks[eid] | 0 : 0;
        if (genId === extraGenerationId) mask |= extraBitflag;
        if ((mask & bitmask) !== bitmask) return false;
    }
    return true;
}

/** Whether an entity's masks contain any bit of the bitmasks. */
export function hasAnyBits(masks: Masks, eid: number, bitmasks: (number | undefined)[]): boolean {
    for (let genId = 0; genId < bitmasks.length; genId++) {
        const bitmask = bitmasks[genId];
        if (!bitmask) continue;
        const genMasks = masks[genId];
        if (genMasks && (genMasks[eid] | 0) & bitmask) return true;
    }
    return false;
}

export function checkAspectConstraints(
    constraints: AspectConstraint[],
    entityMasks: Masks,
    eid: number
): boolean {
    for (let i = 0; i < constraints.length; i++) {
        const constraint = constraints[i];

        if (constraint.type === 'not') {
            if (hasAllBits(entityMasks, eid, constraint.bitmasks)) return false;
            continue;
        }

        if (hasAnyBits(entityMasks, eid, constraint.bitmasks)) continue;

        let hasCompleteAspect = false;
        for (let j = 0; j < constraint.aspects.length; j++) {
            if (hasAllBits(entityMasks, eid, constraint.aspects[j])) {
                hasCompleteAspect = true;
                break;
            }
        }
        if (!hasCompleteAspect) return false;
    }

    return true;
}

/**
 * Whether an aspect tracking group matches using the state accumulated since its
 * tracking modifier was created. Used to populate queries created after the events.
 */
export function checkAspectGroupSinceSnapshot(
    group: TrackingGroup,
    entityMasks: Masks,
    snapshot: Masks,
    dirtyMask: Masks,
    changedMask: Masks,
    eid: number
): boolean {
    const bitmasks = group.bitmasks;
    const isComplete = hasAllBits(entityMasks, eid, bitmasks);

    switch (group.type) {
        case 'add':
            return isComplete && !hasAllBits(snapshot, eid, bitmasks);
        case 'change':
            return isComplete && hasAnyBits(changedMask, eid, bitmasks);
        case 'remove': {
            if (isComplete) return false;
            // Every constituent must have been present at the snapshot or touched since.
            for (let genId = 0; genId < bitmasks.length; genId++) {
                const bitmask = bitmasks[genId];
                if (!bitmask) continue;
                const seen = (snapshot[genId]?.[eid] ?? 0) | (dirtyMask[genId]?.[eid] ?? 0);
                if ((seen & bitmask) !== bitmask) return false;
            }
            return true;
        }
    }
}

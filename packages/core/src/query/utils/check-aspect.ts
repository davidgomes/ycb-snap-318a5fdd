import type { QueryInstance } from '../types';

/**
 * Check if an entity has every trait described by generation-indexed bitmasks.
 */
export function isAspectComplete(
    entityMasks: number[][],
    eid: number,
    bitmasks: (number | undefined)[]
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
 * Check if an entity has at least one bit set in generation-indexed masks.
 */
export function hasAnyBit(
    entityMasks: number[][],
    eid: number,
    bitmasks: (number | undefined)[]
): boolean {
    for (let genId = 0; genId < bitmasks.length; genId++) {
        const mask = bitmasks[genId];
        if (!mask) continue;
        const genMasks = entityMasks[genId];
        const entityMask = genMasks ? genMasks[eid] | 0 : 0;
        if (entityMask & mask) return true;
    }
    return false;
}

export function checkAspectConstraints(
    query: QueryInstance,
    entityMasks: number[][],
    eid: number
): boolean {
    const constraints = query.aspectConstraints;

    for (let i = 0; i < constraints.length; i++) {
        const { type, traitBitmasks, aspectBitmasks } = constraints[i];

        if (type === 'not') {
            for (let j = 0; j < aspectBitmasks.length; j++) {
                if (isAspectComplete(entityMasks, eid, aspectBitmasks[j])) return false;
            }
            continue;
        }

        let matched = hasAnyBit(entityMasks, eid, traitBitmasks);

        for (let j = 0; !matched && j < aspectBitmasks.length; j++) {
            if (isAspectComplete(entityMasks, eid, aspectBitmasks[j])) matched = true;
        }

        if (!matched) return false;
    }

    return true;
}

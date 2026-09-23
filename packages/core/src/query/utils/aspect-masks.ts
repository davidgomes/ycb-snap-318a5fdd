/** True when every set bit in `bitmasks` is set on the entity. */
export function hasAllBits(
    entityMasks: number[][],
    eid: number,
    bitmasks: readonly (number | undefined)[]
): boolean {
    let any = false;

    for (let gen = 0; gen < bitmasks.length; gen++) {
        const mask = (bitmasks[gen] ?? 0) | 0;
        if (!mask) continue;
        any = true;
        const current = (entityMasks[gen]?.[eid] ?? 0) | 0;
        if ((current & mask) !== mask) return false;
    }

    return any;
}

/** Not(aspect) fails the entity when every constituent is present. */
export function failsExclusionGroups(
    exclusionMasks: readonly number[][],
    entityMasks: number[][],
    eid: number
): boolean {
    for (let i = 0; i < exclusionMasks.length; i++) {
        if (hasAllBits(entityMasks, eid, exclusionMasks[i])) return true;
    }
    return false;
}

/**
 * OR constraints that include aspect groups.
 * A loose trait bit or a fully present aspect group satisfies the constraint.
 */
export function matchesDeferredOr(
    orGroupMasks: readonly number[][],
    generations: readonly number[],
    staticBitmasks: readonly { or: number }[],
    entityMasks: number[][],
    eid: number
): boolean {
    for (let i = 0; i < generations.length; i++) {
        const or = staticBitmasks[i]?.or ?? 0;
        if (!or) continue;
        const generationId = generations[i];
        const current = (entityMasks[generationId]?.[eid] ?? 0) | 0;
        if ((current & or) !== 0) return true;
    }

    for (let i = 0; i < orGroupMasks.length; i++) {
        if (hasAllBits(entityMasks, eid, orGroupMasks[i])) return true;
    }

    return false;
}

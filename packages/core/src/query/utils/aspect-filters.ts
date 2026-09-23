import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import type { World } from '../../world';
import type { QueryInstance } from '../types';
import { masksComplete } from './aspect-tracking';

function masksTouchGeneration(groups: number[][], generationId: number): boolean {
    for (let i = 0; i < groups.length; i++) {
        if (groups[i][generationId]) return true;
    }
    return false;
}

/**
 * Static query check for aspects used with Not (missing at least one constituent)
 * and Or (every constituent present counts as one alternative).
 */
export function checkStaticWithGroups(world: World, query: QueryInstance, entity: Entity): boolean {
    const ctx = world[$internal];
    const eid = getEntityId(entity);
    const generations = query.generations;
    const staticBitmasks = query.staticBitmasks;
    const entityMasks = ctx.entityMasks;

    if (query.traitInstances.all.length === 0) return false;

    let hasLooseOr = false;
    let orFailed = false;

    for (let i = 0; i < generations.length; i++) {
        const generationId = generations[i];
        const bitmask = staticBitmasks[i];
        if (!bitmask) continue;

        const required = bitmask.required;
        const forbidden = bitmask.forbidden;
        const or = bitmask.or;
        const entityMask = entityMasks[generationId]?.[eid] || 0;
        const groupGeneration =
            masksTouchGeneration(query.notAllMasks, generationId) ||
            masksTouchGeneration(query.orAllMasks, generationId);

        if (!forbidden && !required && !or && !groupGeneration) return false;
        if (forbidden && (entityMask & forbidden) !== 0) return false;
        if (required && (entityMask & required) !== required) return false;
        if (or !== 0) {
            hasLooseOr = true;
            if ((entityMask & or) === 0) orFailed = true;
        }
    }

    const notAll = query.notAllMasks;
    for (let i = 0; i < notAll.length; i++) {
        if (masksComplete(entityMasks, eid, notAll[i])) return false;
    }

    const orAll = query.orAllMasks;
    if (hasLooseOr || orAll.length > 0) {
        let groupOk = false;
        for (let i = 0; i < orAll.length; i++) {
            if (masksComplete(entityMasks, eid, orAll[i])) {
                groupOk = true;
                break;
            }
        }
        const looseOk = hasLooseOr && !orFailed;
        if (!looseOk && !groupOk) return false;
    }

    return true;
}

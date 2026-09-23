import { $internal } from '../common';
import type { Entity } from '../entity/types';
import type { QueryInstance } from '../query/types';
import type { TraitInstance } from '../trait/types';
import type { World } from './types';

/** When a deferred flush is in progress, record the subscription instead of running it. */
export function noteTraitSub(
    world: World,
    kind: 'add' | 'remove' | 'change',
    instance: TraitInstance,
    entity: Entity,
    target?: Entity
): boolean {
    const log = world[$internal].deferredSubLog;
    if (!log) return false;
    log.push({ kind, instance, entity, target });
    return true;
}

export function noteQuerySub(
    world: World,
    kind: 'query-add' | 'query-remove',
    query: QueryInstance,
    entity: Entity
): boolean {
    const log = world[$internal].deferredSubLog;
    if (!log) return false;
    log.push({ kind, query, entity });
    return true;
}

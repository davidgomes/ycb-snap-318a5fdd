import { $internal } from '../common';
import type { World } from '../world/types';
import type { Predicate, QueryInstance } from './types';

/** Per-world evaluation cache for one predicate. */
export type PredicateRuntime = {
    predicate: Predicate;
    /** 1 when the predicate currently matches the entity id. */
    truth: number[];
    queries: Set<QueryInstance>;
};

export function isPredicateTrue(world: World, predicate: Predicate, eid: number): boolean {
    const runtime = world[$internal].predicateRuntimes.get(predicate.id);
    if (!runtime) return false;
    return runtime.truth[eid] === 1;
}

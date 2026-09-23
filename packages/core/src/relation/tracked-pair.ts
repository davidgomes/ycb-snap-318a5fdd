import type { Trait } from '../trait/types';
import type { Relation, RelationTarget } from './types';

/** A relation pair captured by a tracking modifier. */
export type TrackedRelationPair = {
    relation: Relation<Trait>;
    trait: Trait;
    target: RelationTarget;
};

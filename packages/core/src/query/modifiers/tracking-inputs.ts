import { $internal } from '../../common';
import type { Relation, RelationPair, RelationTarget } from '../../relation/types';
import { isRelation, isRelationPair } from '../../relation/utils/is-relation';
import type { Trait, TraitOrRelation } from '../../trait/types';

/** Arguments accepted by Added / Removed / Changed factories. */
export type TrackingInput = TraitOrRelation | RelationPair;

type PairSchema<T> = T extends RelationPair<infer R>
    ? R
    : T extends Relation<infer R>
      ? R
      : T extends Trait
        ? T
        : never;

/** Underlying traits for a tracking-modifier argument list, preserving tuple positions. */
export type TrackingTraits<T extends readonly TrackingInput[]> = {
    [K in keyof T]: PairSchema<T[K]>;
};

export function normalizeTrackingInputs(inputs: readonly TrackingInput[]): {
    traits: Trait[];
    pairTargets: (RelationTarget | undefined)[];
} {
    const traits: Trait[] = [];
    const pairTargets: (RelationTarget | undefined)[] = [];

    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];
        if (isRelationPair(input)) {
            traits.push(input[$internal].relation[$internal].trait);
            pairTargets.push(input[$internal].target);
        } else if (isRelation(input)) {
            traits.push(input[$internal].trait);
            pairTargets.push(undefined);
        } else {
            traits.push(input);
            pairTargets.push(undefined);
        }
    }

    return { traits, pairTargets };
}

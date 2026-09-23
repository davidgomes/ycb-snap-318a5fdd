import { $internal } from '../../common';
import type { RelationPair } from '../../relation/types';
import { isRelation, isRelationPair } from '../../relation/utils/is-relation';
import type { ExtractTraits, Trait, TraitOrRelation } from '../../trait/types';

export type TrackingParameter = TraitOrRelation | RelationPair;

/** Underlying trait for a tracking argument, including relation pairs. */
export type ExtractTrackingTrait<T> = T extends RelationPair<infer R>
    ? R
    : T extends TraitOrRelation
      ? ExtractTraits<[T]>[number]
      : never;

export type ExtractTrackingTraits<T extends TrackingParameter[]> = {
    [K in keyof T]: ExtractTrackingTrait<T[K]>;
};

export function resolveTrackingInputs<T extends TrackingParameter[]>(inputs: T): {
    traits: ExtractTrackingTraits<T>;
    pairs: RelationPair[];
} {
    const traits: Trait[] = [];
    const pairs: RelationPair[] = [];

    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];
        if (isRelationPair(input)) {
            traits.push(input[$internal].relation[$internal].trait);
            pairs.push(input);
        } else if (isRelation(input)) {
            traits.push(input[$internal].trait);
        } else {
            traits.push(input);
        }
    }

    return { traits: traits as ExtractTrackingTraits<T>, pairs };
}

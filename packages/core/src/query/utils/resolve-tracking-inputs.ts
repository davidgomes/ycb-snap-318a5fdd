import { $internal } from '../../common';
import type { TrackedRelationPair } from '../../relation/tracked-pair';
import type { RelationPair } from '../../relation/types';
import { isRelation, isRelationPair } from '../../relation/utils/is-relation';
import type { Trait, TraitOrRelation } from '../../trait/types';

export type TrackingInput = TraitOrRelation | RelationPair;

export function resolveTrackingInputs(inputs: readonly TrackingInput[]): {
    traits: Trait[];
    pairs: TrackedRelationPair[];
} {
    const traits: Trait[] = [];
    const pairs: TrackedRelationPair[] = [];

    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];

        if (isRelationPair(input)) {
            const pairCtx = input[$internal];
            const relation = pairCtx.relation;
            const trait = relation[$internal].trait;
            traits.push(trait);
            pairs.push({ relation, trait, target: pairCtx.target });
            continue;
        }

        if (isRelation(input)) {
            traits.push(input[$internal].trait);
            continue;
        }

        traits.push(input);
    }

    return { traits, pairs };
}

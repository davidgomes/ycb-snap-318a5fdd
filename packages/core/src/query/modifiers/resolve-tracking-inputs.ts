import { $internal } from '../../common';
import { isRelation, isRelationPair } from '../../relation/utils/is-relation';
import type { RelationPair } from '../../relation/types';
import type { TrackingModifierInput, Trait } from '../../trait/types';

export function resolveTrackingInputs(inputs: readonly TrackingModifierInput[]) {
    const traits: Trait[] = [];
    const pairs: RelationPair[] = [];
    const pairMask: boolean[] = [];
    const trackedTraits: Trait[] = [];

    for (const input of inputs) {
        if (isRelationPair(input)) {
            traits.push(input[$internal].relation[$internal].trait);
            pairs.push(input);
            pairMask.push(true);
            continue;
        }

        const trait = isRelation(input) ? input[$internal].trait : input;
        traits.push(trait);
        trackedTraits.push(trait);
        pairMask.push(false);
    }

    return { traits, pairs, pairMask, trackedTraits };
}

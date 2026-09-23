import { isAspect } from '../../aspect/utils/is-aspect';
import { $internal } from '../../common';
import { isRelationPair } from '../../relation/utils/is-relation';
import type { Relation } from '../../relation/types';
import type { Trait } from '../../trait/types';
import { isModifier } from '../modifier';
import type { QueryHash, QueryParameter } from '../types';

const sortedIDs = new Float64Array(1024); // Use Float64 for larger IDs with relation encoding

// Aspects are negative so they never collide with trait, modifier or pair encodings.
// Modifier ID 0 is reserved for "has", which is how top-level aspects are encoded.
/* @inline @pure */ function encodeAspect(modifierId: number, aspectId: number) {
    return -(modifierId * 100000 + aspectId + 1);
}

export const createQueryHash = (parameters: QueryParameter[]): QueryHash => {
    sortedIDs.fill(0);
    let cursor = 0;

    for (let i = 0; i < parameters.length; i++) {
        const param = parameters[i];

        if (isRelationPair(param)) {
            // Encode relation pair as: (relationTraitId * 1000000) + targetId
            // This ensures unique hashes for different relation/target combinations
            const pairCtx = param[$internal];
            const relation = pairCtx.relation;
            const target = pairCtx.target;

            const relationId = (relation as Relation<Trait>)[$internal].trait.id;
            const targetId = typeof target === 'number' ? target : -1;

            // Combine into a unique hash number
            sortedIDs[cursor++] = relationId * 10000000 + targetId + 5000000;
        } else if (isModifier(param)) {
            const modifierId = param.id;
            const traits = param.traits;

            for (let i = 0; i < traits.length; i++) {
                const trait = traits[i];
                sortedIDs[cursor++] = isAspect(trait)
                    ? encodeAspect(modifierId, trait.id)
                    : modifierId * 100000 + trait.id;
            }
        } else if (isAspect(param)) {
            sortedIDs[cursor++] = encodeAspect(0, param.id);
        } else {
            const traitId = (param as Trait).id;
            sortedIDs[cursor++] = traitId;
        }
    }

    // Sort only the portion of the array that has been filled.
    const filledArray = sortedIDs.subarray(0, cursor);
    filledArray.sort();

    // Create string key.
    const hash = filledArray.join(',');

    return hash;
};

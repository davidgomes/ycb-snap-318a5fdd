import { $internal } from '../../common';
import { isRelationPair } from '../../relation/utils/is-relation';
import type { Relation } from '../../relation/types';
import type { Trait } from '../../trait/types';
import { isModifier, isOrWithModifiers } from '../modifier';
import type { QueryHash, QueryParameter } from '../types';

const sortedIDs = new Float64Array(1024); // Use Float64 for larger IDs with relation encoding

export const createQueryHash = (parameters: QueryParameter[]): QueryHash => {
    sortedIDs.fill(0);
    let cursor = 0;
    // Parameters that cannot be encoded as a single number, such as tracked pairs
    const keys: string[] = [];

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
            const traitIds = param.traitIds;
            const pairTargets = param.pairTargets;

            for (let i = 0; i < traitIds.length; i++) {
                const traitId = traitIds[i];
                const target = pairTargets?.[i];
                if (target !== undefined) keys.push(`${modifierId}:${traitId}:${target}`);
                else sortedIDs[cursor++] = modifierId * 100000 + traitId;
            }

            if (isOrWithModifiers(param)) {
                for (const nested of param.modifiers) {
                    for (let j = 0; j < nested.traitIds.length; j++) {
                        const target = nested.pairTargets?.[j];
                        keys.push(
                            `or:${nested.id}:${nested.traitIds[j]}` +
                                (target !== undefined ? `:${target}` : '')
                        );
                    }
                }
            }
        } else {
            const traitId = (param as Trait).id;
            sortedIDs[cursor++] = traitId;
        }
    }

    // Sort only the portion of the array that has been filled.
    const filledArray = sortedIDs.subarray(0, cursor);
    filledArray.sort();

    // Create string key.
    let hash = filledArray.join(',');
    if (keys.length > 0) hash += '|' + keys.sort().join(',');

    return hash;
};

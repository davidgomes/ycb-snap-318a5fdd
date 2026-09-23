import { $internal } from '../../common';
import { isRelationPair } from '../../relation/utils/is-relation';
import type { Relation } from '../../relation/types';
import type { Trait } from '../../trait/types';
import { isModifier, isOrWithModifiers } from '../modifier';
import type { Modifier, QueryHash, QueryParameter } from '../types';

const sortedIDs = new Float64Array(1024); // Use Float64 for larger IDs with relation encoding

export const createQueryHash = (parameters: QueryParameter[]): QueryHash => {
    sortedIDs.fill(0);
    let cursor = 0;
    const pairKeys: string[] = [];

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

            for (let i = 0; i < traitIds.length; i++) {
                const traitId = traitIds[i];
                sortedIDs[cursor++] = modifierId * 100000 + traitId;
            }

            addPairKeys(param, '', pairKeys);
            if (isOrWithModifiers(param)) {
                for (const nested of param.modifiers) addPairKeys(nested, 'or', pairKeys);
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
    if (pairKeys.length > 0) hash += '|' + pairKeys.sort().join(',');

    return hash;
};

function addPairKeys(modifier: Modifier, prefix: string, keys: string[]) {
    if (!modifier.pairs) return;
    for (const pair of modifier.pairs) {
        const { relation, target } = pair[$internal];
        keys.push(`${prefix}${modifier.type}:${relation[$internal].trait.id}:${target}`);
    }
}

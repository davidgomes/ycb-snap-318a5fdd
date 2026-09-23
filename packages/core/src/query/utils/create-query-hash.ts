import { $internal } from '../../common';
import { isRelationPair } from '../../relation/utils/is-relation';
import type { Relation } from '../../relation/types';
import type { Trait } from '../../trait/types';
import { isModifier, isOrWithModifiers } from '../modifier';
import { isPredicate } from '../predicate';
import type { Modifier, QueryHash, QueryParameter } from '../types';

const sortedIDs = new Float64Array(1024); // Use Float64 for larger IDs with relation encoding

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
        } else if (isPredicate(param)) {
            // Distinct from trait ids and modifier/trait pairs.
            sortedIDs[cursor++] = 2_000_000_000_000 + param.id;
        } else if (isModifier(param)) {
            cursor = writeModifierHash(param, sortedIDs, cursor);
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

function writeModifierHash(param: Modifier, ids: Float64Array, cursor: number): number {
    const modifierId = param.id;
    const traitIds = param.traitIds;

    for (let i = 0; i < traitIds.length; i++) {
        ids[cursor++] = modifierId * 100000 + traitIds[i];
    }

    const predicates = param.predicates;
    if (predicates) {
        for (let i = 0; i < predicates.length; i++) {
            ids[cursor++] = 1_000_000_000_000 + modifierId * 1_000_000 + predicates[i].id;
        }
    }

    if (isOrWithModifiers(param)) {
        for (let i = 0; i < param.modifiers.length; i++) {
            cursor = writeModifierHash(param.modifiers[i], ids, cursor);
        }
    }

    return cursor;
}

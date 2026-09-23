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

    // Pair targets must keep queries distinct even when the relation trait id matches.
    const pairParts: string[] = [];
    for (let i = 0; i < parameters.length; i++) collectPairHash(parameters[i], pairParts);
    if (pairParts.length > 0) {
        pairParts.sort();
        hash += ';' + pairParts.join('|');
    }

    return hash;
};

function collectPairHash(param: QueryParameter, parts: string[]): void {
    if (!isModifier(param)) return;

    const pairs = (param as Modifier).pairs;
    if (pairs) {
        for (let i = 0; i < pairs.length; i++) {
            const pairCtx = pairs[i][$internal];
            const traitId = (pairCtx.relation as Relation<Trait>)[$internal].trait.id;
            const target = pairCtx.target === '*' ? '*' : pairCtx.target;
            parts.push(`${param.type}:${param.id}:${traitId}:${target}`);
        }
    }

    if (isOrWithModifiers(param)) {
        for (let i = 0; i < param.modifiers.length; i++) collectPairHash(param.modifiers[i], parts);
    }
}

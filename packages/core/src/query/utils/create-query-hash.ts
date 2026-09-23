import { $internal } from '../../common';
import { isRelationPair } from '../../relation/utils/is-relation';
import type { Relation } from '../../relation/types';
import type { Trait } from '../../trait/types';
import { isModifier, isOrWithModifiers } from '../modifier';
import type { Modifier, QueryHash, QueryParameter } from '../types';

const sortedIDs = new Float64Array(1024); // Use Float64 for larger IDs with relation encoding
const pairParts: string[] = [];

function hashModifier(modifier: Modifier): void {
    const modifierId = modifier.id;
    const traitIds = modifier.traitIds;
    const pairTargets = modifier.pairTargets;

    for (let i = 0; i < traitIds.length; i++) {
        sortedIDs[cursor++] = modifierId * 100000 + traitIds[i];
        const target = pairTargets?.[i];
        if (target === undefined) continue;
        pairParts.push(`${modifierId}:${traitIds[i]}:${target === '*' ? '*' : target}`);
    }

    if (isOrWithModifiers(modifier)) {
        const nested = modifier.modifiers;
        for (let i = 0; i < nested.length; i++) hashModifier(nested[i]);
    }
}

let cursor = 0;

export const createQueryHash = (parameters: QueryParameter[]): QueryHash => {
    sortedIDs.fill(0);
    pairParts.length = 0;
    cursor = 0;

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
            hashModifier(param);
        } else {
            const traitId = (param as Trait).id;
            sortedIDs[cursor++] = traitId;
        }
    }

    // Sort only the portion of the array that has been filled.
    const filledArray = sortedIDs.subarray(0, cursor);
    filledArray.sort();

    pairParts.sort();
    const hash = filledArray.join(',') + (pairParts.length ? `#${pairParts.join(',')}` : '');

    return hash;
};

import { $internal } from '../../common';
import { isRelationPair } from '../../relation/utils/is-relation';
import type { Relation, RelationPair } from '../../relation/types';
import type { Trait } from '../../trait/types';
import { isModifier, isOrWithModifiers } from '../modifier';
import type { QueryHash, QueryParameter } from '../types';

const sortedIDs = new Float64Array(1024); // Use Float64 for larger IDs with relation encoding

export const createQueryHash = (parameters: QueryParameter[]): QueryHash => {
    sortedIDs.fill(0);
    let cursor = 0;
    const tokens: string[] = [];

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
            const pairs = param.pairs;

            for (let i = 0; i < traitIds.length; i++) {
                const traitId = traitIds[i];
                const pair = pairs?.[i];
                if (pair) tokens.push(getPairToken(modifierId, traitId, pair));
                else sortedIDs[cursor++] = modifierId * 100000 + traitId;
            }

            if (isOrWithModifiers(param) && param.modifiers.length > 0) {
                const nested: string[] = [];
                for (const modifier of param.modifiers) {
                    for (let i = 0; i < modifier.traitIds.length; i++) {
                        const pair = modifier.pairs?.[i];
                        nested.push(
                            pair
                                ? getPairToken(modifier.id, modifier.traitIds[i], pair)
                                : `${modifier.id * 100000 + modifier.traitIds[i]}`
                        );
                    }
                }
                tokens.push(`or(${nested.sort().join(',')})`);
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
    const hash = filledArray.join(',');
    if (tokens.length === 0) return hash;

    return `${hash}|${tokens.sort().join('|')}`;
};

function getPairToken(modifierId: number, traitId: number, pair: RelationPair): string {
    return `p${modifierId}:${traitId}:${pair[$internal].target}`;
}

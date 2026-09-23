import { $internal } from '../../common';
import { isRelationPair } from '../../relation/utils/is-relation';
import type { Relation } from '../../relation/types';
import type { Trait } from '../../trait/types';
import { isModifier, isOrWithModifiers } from '../modifier';
import type { Modifier, QueryHash, QueryParameter } from '../types';

const sortedIDs = new Float64Array(1024); // Use Float64 for larger IDs with relation encoding

/**
 * Entries that can't be packed into a single number: pairs tracked by a modifier
 * (modifier, relation and target) and modifiers nested in `Or`.
 */
function pushModifierTokens(tokens: string[], modifier: Modifier, prefix: string, pairsOnly: boolean) {
    const traitIds = modifier.traitIds;
    const pairs = modifier.pairs;

    for (let i = 0; i < traitIds.length; i++) {
        const pair = pairs?.[i];
        if (pair) tokens.push(`${prefix}${modifier.id}:${traitIds[i]}:${pair[$internal].target}`);
        else if (!pairsOnly) tokens.push(`${prefix}${modifier.id}:${traitIds[i]}`);
    }
}

export const createQueryHash = (parameters: QueryParameter[]): QueryHash => {
    sortedIDs.fill(0);
    let cursor = 0;
    let tokens: string[] | undefined;

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
                if (pairs?.[i]) continue;
                const traitId = traitIds[i];
                sortedIDs[cursor++] = modifierId * 100000 + traitId;
            }

            if (pairs) pushModifierTokens((tokens ??= []), param, '', true);

            if (isOrWithModifiers(param)) {
                for (const nested of param.modifiers) {
                    pushModifierTokens((tokens ??= []), nested, 'or:', false);
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
    if (tokens) hash += '|' + tokens.sort().join(',');

    return hash;
};

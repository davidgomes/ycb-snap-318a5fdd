import { $internal } from '../../common';
import { isRelationPair } from '../../relation/utils/is-relation';
import type { Relation, RelationPair } from '../../relation/types';
import type { Trait } from '../../trait/types';
import { isModifier, isOrWithModifiers } from '../modifier';
import type { Modifier, QueryHash, QueryParameter } from '../types';

const sortedIDs = new Float64Array(1024); // Use Float64 for larger IDs with relation encoding

function isTrackingModifierType(param: Modifier) {
    return (
        param.type.includes('added') || param.type.includes('removed') || param.type.includes('changed')
    );
}

function richModifierToken(param: Modifier): string {
    const parts: string[] = [param.type];
    const tracked = param.trackedTraitIds ?? param.traitIds;
    const traitTokens = tracked.map((id) => `t${id}`).sort();
    parts.push(...traitTokens);

    if (param.pairs?.length) {
        const pairTokens = param.pairs.map((pair) => pairToken(pair)).sort();
        parts.push(...pairTokens);
    }

    if (isOrWithModifiers(param)) {
        const nested = param.modifiers.map((modifier) => `(${richModifierToken(modifier)})`).sort();
        parts.push(...nested);
    }

    return parts.join('|');
}

function pairToken(pair: RelationPair) {
    const relationId = (pair[$internal].relation as Relation<Trait>)[$internal].trait.id;
    const target = pair[$internal].target;
    const targetId = typeof target === 'number' ? target : '*';
    return `p${relationId}:${targetId}`;
}

function appendModifier(param: Modifier, extras: string[]) {
    const hasNestedTracking =
        isOrWithModifiers(param) && param.modifiers.some((modifier) => isTrackingModifierType(modifier));

    if ((param.pairs && param.pairs.length > 0) || hasNestedTracking) {
        extras.push(richModifierToken(param));
        return;
    }

    const modifierId = param.id;
    const traitIds = param.trackedTraitIds ?? param.traitIds;
    for (let i = 0; i < traitIds.length; i++) {
        sortedIDs[cursor++] = modifierId * 100000 + traitIds[i];
    }
}

let cursor = 0;

export const createQueryHash = (parameters: QueryParameter[]): QueryHash => {
    sortedIDs.fill(0);
    cursor = 0;
    const extras: string[] = [];

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
            appendModifier(param, extras);
        } else {
            const traitId = (param as Trait).id;
            sortedIDs[cursor++] = traitId;
        }
    }

    // Sort only the portion of the array that has been filled.
    const filledArray = sortedIDs.subarray(0, cursor);
    filledArray.sort();

    const numeric = filledArray.join(',');
    if (!extras.length) return numeric;

    extras.sort();
    return numeric.length > 0 ? `${numeric}|${extras.join(',')}` : extras.join(',');
};

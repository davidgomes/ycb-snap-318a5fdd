import { $internal } from '../../common';
import { isRelation, isRelationPair } from '../../relation/utils/is-relation';
import type { ExtractTraits, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';

export function createRemoved() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
        world[$internal].resetSubscriptions.add((resetWorld) => setTrackingMasks(resetWorld, id));
    }

    return <T extends TraitOrRelation[]>(
        ...inputs: T
    ): Modifier<ExtractTraits<T>, `removed-${number}`> => {
        const pairs = inputs.filter(isRelationPair);
        const traits = inputs.map((input) =>
            isRelation(input)
                ? input[$internal].trait
                : isRelationPair(input)
                  ? input[$internal].relation[$internal].trait
                  : input
        ) as ExtractTraits<T>;
        const modifier = createModifier(`removed-${id}`, id, traits);
        if (pairs.length) {
            modifier.relationPairs = pairs;
            modifier.relationPairIndices = inputs.reduce<number[]>(
                (indices, input, index) => (isRelationPair(input) ? indices.concat(index) : indices),
                []
            );
        }
        return modifier;
    };
}

import { $internal } from '../../common';
import { isRelation } from '../../relation/utils/is-relation';
import type { ExtractTraits, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';
import { isPredicate, wrapPredicate } from '../predicate';

export function createAdded() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends TraitOrRelation[]>(
        ...inputs: T
    ): Modifier<ExtractTraits<T>, `added-${number}`> => {
        if (inputs.length === 1 && isPredicate(inputs[0])) {
            const predicate = inputs[0];
            return wrapPredicate(predicate, predicate.predicate, 'added') as Modifier<
                ExtractTraits<T>,
                `added-${number}`
            >;
        }
        const traits = inputs.map((input) =>
            isRelation(input) ? input[$internal].trait : input
        ) as ExtractTraits<T>;
        return createModifier(`added-${id}`, id, traits);
    };
}

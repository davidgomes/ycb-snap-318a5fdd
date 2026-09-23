import { $internal } from '../../common';
import type { Predicate } from '../../predicate/types';
import { isPredicate } from '../../predicate/utils/is-predicate';
import { isRelation } from '../../relation/utils/is-relation';
import type { ExtractTraits, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';

export function createAdded() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends (TraitOrRelation | Predicate)[]>(
        ...inputs: T
    ): Modifier<ExtractTraits<T>, `added-${number}`> => {
        const traits = inputs.map((input) =>
            isRelation(input) || isPredicate(input) ? input[$internal].trait : input
        ) as ExtractTraits<T>;
        return createModifier(`added-${id}`, id, traits);
    };
}

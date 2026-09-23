import type { Aspect } from '../../aspect/types';
import { $internal } from '../../common';
import { isRelation } from '../../relation/utils/is-relation';
import type { ExtractModifierTraits, Trait, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createAspectAwareModifier } from '../modifier';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';

export function createRemoved() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends (TraitOrRelation | Aspect)[]>(
        ...inputs: T
    ): Modifier<ExtractModifierTraits<T>, `removed-${number}`> => {
        const traits = inputs.map((input) =>
            isRelation(input) ? input[$internal].trait : input
        ) as (Trait | Aspect)[];
        return createAspectAwareModifier(`removed-${id}`, id, traits) as Modifier<
            ExtractModifierTraits<T>,
            `removed-${number}`
        >;
    };
}

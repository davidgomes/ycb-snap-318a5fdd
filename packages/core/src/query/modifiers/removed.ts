import { $internal } from '../../common';
import { isAspect } from '../../aspect/aspect';
import type { Aspect } from '../../aspect/types';
import { isRelation } from '../../relation/utils/is-relation';
import type { ExtractTraits, Trait, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
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
    ): Modifier<ExtractTraits<T>, `removed-${number}`> => {
        if (inputs.length === 1 && isAspect(inputs[0])) {
            const aspect = inputs[0] as Aspect;
            const modifier = createModifier(`removed-${id}`, id, [...aspect.traits]) as Modifier<
                Trait[],
                `removed-${number}`
            >;
            modifier.aspect = aspect;
            return modifier as Modifier<ExtractTraits<T>, `removed-${number}`>;
        }

        const traits = inputs.map((input) =>
            isRelation(input) ? input[$internal].trait : input
        ) as ExtractTraits<T>;
        return createModifier(`removed-${id}`, id, traits);
    };
}

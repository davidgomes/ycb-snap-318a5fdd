import { $internal } from '../../common';
import { isAspect } from '../../aspect/aspect';
import { isRelation } from '../../relation/utils/is-relation';
import type { ExtractTraits, TraitOrRelation } from '../../trait/types';
import type { Aspect } from '../../aspect/types';
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

    return <T extends (TraitOrRelation | Aspect)[]>(
        ...inputs: T
    ): Modifier<ExtractTraits<T>, `added-${number}`> => {
        if (inputs.length === 1 && isAspect(inputs[0])) {
            const aspect = inputs[0] as Aspect;
            return createModifier(`added-${id}`, id, [...aspect.traits]);
        }

        const traits = inputs.map((input) =>
            isRelation(input) ? input[$internal].trait : input
        ) as ExtractTraits<T>;
        return createModifier(`added-${id}`, id, traits);
    };
}

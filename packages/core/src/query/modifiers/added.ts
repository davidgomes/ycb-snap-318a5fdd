import type { RelationPair } from '../../relation/types';
import type { ExtractTraits, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createTrackingModifier } from '../modifier';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';

export function createAdded() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends (TraitOrRelation | RelationPair)[]>(
        ...inputs: T
    ): Modifier<ExtractTraits<T>, `added-${number}`> => {
        return createTrackingModifier(`added-${id}`, id, inputs) as Modifier<
            ExtractTraits<T>,
            `added-${number}`
        >;
    };
}

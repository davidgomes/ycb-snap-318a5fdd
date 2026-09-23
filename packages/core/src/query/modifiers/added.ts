import type { ExtractTraits, TrackingModifierInput } from '../../trait/types';
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

    return <T extends TrackingModifierInput[]>(
        ...inputs: T
    ): Modifier<ExtractTraits<T>, `added-${number}`> =>
        createTrackingModifier(`added-${id}` as `added-${number}`, id, inputs);
}

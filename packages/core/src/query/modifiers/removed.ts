import type { ExtractTraits, TrackingModifierInput } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createTrackingModifier } from '../modifier';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';

export function createRemoved() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends TrackingModifierInput[]>(
        ...inputs: T
    ): Modifier<ExtractTraits<T>, `removed-${number}`> =>
        createTrackingModifier(`removed-${id}` as `removed-${number}`, id, inputs);
}

import type { ExtractTrackingTraits, TrackingModifierInput } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';
import { resolveTrackingInputs } from './resolve-tracking-inputs';

export function createRemoved() {
    const id = createTrackingId('remove');

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends TrackingModifierInput[]>(
        ...inputs: T
    ): Modifier<ExtractTrackingTraits<T>, `removed-${number}`> => {
        const resolved = resolveTrackingInputs(inputs);
        return createModifier(`removed-${id}`, id, resolved.traits as ExtractTrackingTraits<T>, resolved);
    };
}

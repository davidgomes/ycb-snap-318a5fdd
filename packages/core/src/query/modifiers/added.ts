import type { ExtractTrackingTraits, TrackingModifierInput } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';
import { resolveTrackingInputs } from './resolve-tracking-inputs';

export function createAdded() {
    const id = createTrackingId('add');

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends TrackingModifierInput[]>(
        ...inputs: T
    ): Modifier<ExtractTrackingTraits<T>, `added-${number}`> => {
        const resolved = resolveTrackingInputs(inputs);
        return createModifier(`added-${id}`, id, resolved.traits as ExtractTrackingTraits<T>, resolved);
    };
}

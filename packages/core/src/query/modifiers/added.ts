import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';
import { normalizeTrackingInputs, type TrackingInput, type TrackingTraits } from './tracking-inputs';

export function createAdded() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends TrackingInput[]>(...inputs: T): Modifier<TrackingTraits<T>, `added-${number}`> => {
        const { traits, pairTargets } = normalizeTrackingInputs(inputs);
        return createModifier(`added-${id}`, id, traits, pairTargets) as Modifier<
            TrackingTraits<T>,
            `added-${number}`
        >;
    };
}

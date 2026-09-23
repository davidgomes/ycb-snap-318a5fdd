import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';
import {
    type ExtractTrackingTraits,
    type TrackingParameter,
    resolveTrackingInputs,
} from './tracking-inputs';

export function createAdded() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends TrackingParameter[]>(
        ...inputs: T
    ): Modifier<ExtractTrackingTraits<T>, `added-${number}`> => {
        const { traits, pairs } = resolveTrackingInputs(inputs);
        return createModifier(`added-${id}`, id, traits, pairs);
    };
}

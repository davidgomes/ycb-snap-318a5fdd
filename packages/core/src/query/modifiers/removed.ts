import type { RelationPair } from '../../relation/types';
import type { ExtractTrait } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';
import { resolveTrackingInputs, type TrackingInput } from '../utils/resolve-tracking-inputs';

type ExtractTracking<T> = T extends RelationPair<infer R> ? R : ExtractTrait<T>;

export function createRemoved() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends TrackingInput[]>(
        ...inputs: T
    ): Modifier<{ [K in keyof T]: ExtractTracking<T[K]> }, `removed-${number}`> => {
        const { traits, pairs } = resolveTrackingInputs(inputs);
        return createModifier(
            `removed-${id}`,
            id,
            traits as { [K in keyof T]: ExtractTracking<T[K]> },
            pairs
        );
    };
}

import { splitModifierInputs, type FlattenInputs } from '../../aspect/aspect';
import type { Trait } from '../../trait/types';
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

    return <const T extends readonly unknown[]>(
        ...inputs: T
    ): Modifier<
        FlattenInputs<T> extends readonly Trait[] ? [...FlattenInputs<T>] : Trait[],
        `added-${number}`
    > => {
        const { traits, aspects, terms } = splitModifierInputs(inputs);
        return createModifier(`added-${id}`, id, traits, aspects, terms) as Modifier<
            FlattenInputs<T> extends readonly Trait[] ? [...FlattenInputs<T>] : Trait[],
            `added-${number}`
        >;
    };
}

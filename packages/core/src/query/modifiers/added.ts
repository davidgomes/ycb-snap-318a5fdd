import type { Aspect } from '../../aspect/types';
import type { Relation } from '../../relation/types';
import type { Trait } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import { collectModifierInputs, modifierExtras, type ModifierFromInputs } from '../modifier-inputs';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';

export function createAdded() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends readonly (Trait | Relation<Trait> | Aspect)[]>(
        ...inputs: T
    ): ModifierFromInputs<T, `added-${number}`> => {
        const collected = collectModifierInputs(inputs);
        return createModifier(
            `added-${id}`,
            id,
            collected.traits,
            modifierExtras(collected)
        ) as ModifierFromInputs<T, `added-${number}`>;
    };
}

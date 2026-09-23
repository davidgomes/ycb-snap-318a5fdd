import type { Aspect } from '../../aspect/types';
import type { Trait } from '../../trait/types';
import { createModifier } from '../modifier';
import { collectModifierInputs, modifierExtras, type ModifierFromInputs } from '../modifier-inputs';

export const Not = <T extends readonly (Trait | Aspect)[]>(
    ...inputs: T
): ModifierFromInputs<T, 'not'> => {
    const collected = collectModifierInputs(inputs);
    return createModifier('not', 1, collected.traits, modifierExtras(collected)) as ModifierFromInputs<
        T,
        'not'
    >;
};

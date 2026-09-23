import { splitModifierInputs } from '../../aspect/split-modifier-inputs';
import type { Aspect } from '../../aspect/types';
import type { Trait } from '../../trait/types';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';

export const Not = <T extends readonly (Trait | Aspect)[] = Trait[]>(
    ...inputs: T
): Modifier<Trait[], 'not', T> => {
    const { traits, sources } = splitModifierInputs(inputs);
    return createModifier('not', 1, traits, sources as unknown as T);
};

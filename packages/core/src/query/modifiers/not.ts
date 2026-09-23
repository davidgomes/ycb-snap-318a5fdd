import { splitModifierInputs } from '../../aspect/aspect';
import type { Trait } from '../../trait/types';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';

export const Not = (...inputs: unknown[]): Modifier<Trait[], 'not'> => {
    const { traits, aspects, terms } = splitModifierInputs(inputs);
    return createModifier('not', 1, traits, aspects, terms);
};

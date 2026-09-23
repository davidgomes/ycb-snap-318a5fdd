import type { Modifier, ModifierTrait } from '../types';
import { createModifier } from '../modifier';

export const Not = <T extends ModifierTrait[] = ModifierTrait[]>(
    ...traits: T
): Modifier<T, 'not'> => {
    return createModifier('not', 1, traits);
};

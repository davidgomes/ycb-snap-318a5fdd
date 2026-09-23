import type { Modifier, ModifierInput } from '../types';
import { createModifier } from '../modifier';

export const Not = <T extends ModifierInput[] = ModifierInput[]>(
    ...traits: T
): Modifier<T, 'not'> => {
    return createModifier('not', 1, traits);
};

import type { Modifier, ModifierMember } from '../types';
import { createModifier } from '../modifier';

export const Not = <T extends ModifierMember[] = ModifierMember[]>(
    ...traits: T
): Modifier<T, 'not'> => {
    return createModifier('not', 1, traits);
};

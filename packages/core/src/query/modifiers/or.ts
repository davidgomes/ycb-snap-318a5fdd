import type { Modifier, ModifierTrait, OrModifier, OrParameter } from '../types';
import { $modifier, createModifier } from '../modifier';

export const Or = <T extends OrParameter[]>(...params: T): OrModifier<T> => {
    // Separate traits and aspects from nested modifiers
    const traits: ModifierTrait[] = [];
    const modifiers: Modifier[] = [];

    for (const param of params) {
        if ((param as Modifier)[$modifier]) {
            modifiers.push(param as Modifier);
        } else {
            traits.push(param as ModifierTrait);
        }
    }

    const modifier = createModifier('or', 2, traits) as OrModifier<T>;
    modifier.modifiers = modifiers;

    return modifier;
};

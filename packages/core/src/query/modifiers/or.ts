import type { Modifier, ModifierMember, OrModifier, OrParameter } from '../types';
import { $modifier, createModifier } from '../modifier';

export const Or = <T extends OrParameter[]>(...params: T): OrModifier<T> => {
    // Separate traits and aspects from nested modifiers
    const traits: ModifierMember[] = [];
    const modifiers: Modifier<any>[] = [];

    for (const param of params) {
        if ((param as Modifier)[$modifier]) {
            modifiers.push(param as Modifier);
        } else {
            traits.push(param as ModifierMember);
        }
    }

    const modifier = createModifier('or', 2, traits) as OrModifier<T>;
    modifier.modifiers = modifiers;

    return modifier;
};

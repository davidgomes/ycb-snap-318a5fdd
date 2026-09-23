import type { Trait } from '../../trait/types';
import type { Modifier, OrModifier, OrParameter, Predicate } from '../types';
import { $modifier, createModifier } from '../modifier';
import { isPredicate } from '../predicate';

export const Or = <T extends OrParameter[]>(...params: T): OrModifier<T> => {
    // Separate traits from nested modifiers and predicates
    const traits: Trait[] = [];
    const modifiers: Modifier[] = [];
    const predicates: Predicate[] = [];

    for (const param of params) {
        if (isPredicate(param)) {
            predicates.push(param);
        } else if ((param as Modifier)[$modifier]) {
            modifiers.push(param as Modifier);
        } else {
            traits.push(param as Trait);
        }
    }

    const modifier = createModifier('or', 2, traits) as OrModifier<T>;
    modifier.modifiers = modifiers;
    if (predicates.length) modifier.predicates = predicates;

    return modifier;
};

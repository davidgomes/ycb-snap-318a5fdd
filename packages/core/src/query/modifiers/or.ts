import type { Trait } from '../../trait/types';
import type { Modifier, OrModifier, OrParameter } from '../types';
import { $modifier, createModifier } from '../modifier';
import { isPredicate, wrapPredicate } from '../predicate';

export const Or = <T extends OrParameter[]>(...params: T): OrModifier<T> => {
    const predicates = params.filter(isPredicate);
    if (predicates.length) {
        const dependencies = predicates.flatMap((predicate) => predicate.traits);
        const modifier = createModifier('predicate', 2, dependencies);
        modifier.predicate = (data) => {
            let offset = 0;
            return predicates.some((predicate) => {
                const values = data.slice(offset, offset + predicate.traits.length);
                offset += predicate.traits.length;
                return predicate.predicate(values);
            });
        };
        modifier.predicateMode = 'current';
        return modifier as OrModifier<T>;
    }
    // Separate traits from nested modifiers
    const traits: Trait[] = [];
    const modifiers: Modifier[] = [];

    for (const param of params) {
        if ((param as Modifier)[$modifier]) {
            modifiers.push(param as Modifier);
        } else {
            traits.push(param as Trait);
        }
    }

    const modifier = createModifier('or', 2, traits) as OrModifier<T>;
    modifier.modifiers = modifiers;

    return modifier;
};

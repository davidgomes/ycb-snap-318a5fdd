import type { Trait } from '../../trait/types';
import type { Modifier, PredicateModifier } from '../types';
import { createModifier } from '../modifier';
import { isPredicateModifier } from './predicate';

export const Not = <T extends Trait[] = Trait[]>(
    ...items: (Trait | PredicateModifier)[]
): Modifier<T, 'not'> => {
    const traits: Trait[] = [];
    const predicates: PredicateModifier[] = [];

    for (const item of items) {
        if (isPredicateModifier(item)) predicates.push(item);
        else traits.push(item as Trait);
    }

    const modifier = createModifier('not', 1, traits as T);
    if (predicates.length > 0) modifier.predicates = predicates;
    return modifier;
};

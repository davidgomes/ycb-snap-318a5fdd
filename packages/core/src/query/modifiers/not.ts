import type { Trait } from '../../trait/types';
import type { Modifier } from '../types';
import { createModifier } from '../modifier';
import { isPredicate, wrapPredicate } from '../predicate';

export const Not = <T extends (Trait | Modifier)[] = Trait[]>(...traits: T): Modifier<T, 'not'> => {
    if (traits.length === 1 && isPredicate(traits[0])) {
        const predicate = traits[0];
        return wrapPredicate(predicate, (data) => !predicate.predicate(data));
    }
    return createModifier('not', 1, traits);
};

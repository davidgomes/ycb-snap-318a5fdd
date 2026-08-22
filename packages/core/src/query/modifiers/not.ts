import type { Trait } from '../../trait/types';
import type { Modifier } from '../types';
import { createModifier } from '../modifier';
import { withPredicateMode } from './predicate';

export const Not = <T extends Trait[] = Trait[]>(...traits: T): Modifier<T, 'not'> => {
    if (traits.length === 1 && (traits[0] as Modifier).predicate) {
        return withPredicateMode(traits[0] as Modifier<[], 'predicate'>, 'not') as Modifier<T, 'not'>;
    }
    return createModifier('not', 1, traits);
};

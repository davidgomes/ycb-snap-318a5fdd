import type { Trait } from '../../trait/types';
import { createModifier } from '../modifier';
import { splitModifierInputs } from '../predicate';
import type { Modifier, Predicate } from '../types';

export function Not<T extends Trait[]>(...traits: T): Modifier<T, 'not'>;
export function Not(...inputs: Array<Trait | Predicate>): Modifier;
export function Not(...inputs: Array<Trait | Predicate>): Modifier {
    const { traits, predicates } = splitModifierInputs(inputs, false);
    const modifier = createModifier('not', 1, traits as Trait[]);
    if (predicates.length > 0) modifier.predicates = predicates;
    return modifier;
}

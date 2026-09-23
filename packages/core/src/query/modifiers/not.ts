import type { Trait } from '../../trait/types';
import { isPredicate, type Predicate } from '../predicate';
import type { Modifier } from '../types';
import { createModifier } from '../modifier';

export function Not<T extends Trait[]>(...traits: T): Modifier<T, 'not'>;
export function Not(...inputs: Array<Trait | Predicate>): Modifier;
export function Not(...inputs: Array<Trait | Predicate>): Modifier {
    const traits: Trait[] = [];
    const predicates: Predicate[] = [];

    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];
        if (isPredicate(input)) predicates.push(input);
        else traits.push(input);
    }

    const modifier = createModifier('not', 1, traits);
    modifier.predicates = predicates;
    return modifier;
}

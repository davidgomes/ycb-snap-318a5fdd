import { createModifier, isPredicate } from '../modifier';
import type { Trait } from '../../trait/types';
import type { Modifier, Predicate } from '../types';

export function Not<T extends Trait[]>(...traits: T): Modifier<T, 'not'>;
export function Not(predicate: Predicate): Modifier<[], 'not'>;
export function Not(...inputs: Array<Trait | Predicate>): Modifier<any, 'not'> {
    const traits: Trait[] = [];
    const predicates: Predicate[] = [];

    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];
        if (isPredicate(input)) predicates.push(input);
        else traits.push(input);
    }

    const modifier = createModifier('not', 1, traits as Trait[]);
    if (predicates.length) modifier.predicates = predicates;
    return modifier;
}

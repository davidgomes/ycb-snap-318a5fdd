import type { Trait } from '../../trait/types';
import { createModifier } from '../modifier';
import { isPredicate } from '../predicate';
import type { Modifier, Predicate } from '../types';

export function Not(predicate: Predicate): Modifier<[], 'not'>;
export function Not<T extends Trait[]>(...traits: T): Modifier<T, 'not'>;
export function Not(...params: (Trait | Predicate)[]): Modifier<any, 'not'> {
    if (params.length === 1 && isPredicate(params[0])) {
        const modifier = createModifier('not', 1, [] as unknown as Trait[]);
        modifier.predicates = [params[0]];
        return modifier;
    }
    return createModifier('not', 1, params as Trait[]);
}

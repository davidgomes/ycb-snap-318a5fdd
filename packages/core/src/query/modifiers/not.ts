import type { Trait } from '../../trait/types';
import type { Modifier } from '../types';
import { createModifier } from '../modifier';
import { isPredicate, type Predicate, wrapPredicate } from '../predicate';

export function Not<T extends Trait[]>(predicate: Predicate<T>): Predicate<T, 'not'>;
export function Not<T extends Trait[] = Trait[]>(...traits: T): Modifier<T, 'not'>;
export function Not(...traits: any[]): any {
    if (traits.length === 1 && isPredicate(traits[0])) return wrapPredicate(traits[0], 'not');
    return createModifier('not', 1, traits);
}

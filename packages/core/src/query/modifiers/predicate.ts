import { $internal } from '../../common';
import { isRelation } from '../../relation/utils/is-relation';
import type { Trait } from '../../trait/types';
import type { Modifier, Predicate } from '../types';
import { createModifier } from '../modifier';

let predicateId = 0;

export function createPredicate<T extends Trait[]>(
    dependencies: T,
    test: (data: unknown[]) => boolean
): Modifier<[], 'predicate'> {
    for (const dependency of dependencies) {
        if (isRelation(dependency) || dependency[$internal].type === 'tag') {
            throw new Error('Predicate dependencies must be data traits, not tags or relations');
        }
    }

    const predicate: Predicate = { dependencies, test };
    return createModifier('predicate', ++predicateId, [], predicate);
}

export function withPredicateMode(
    predicate: Modifier<[], 'predicate'>,
    mode: NonNullable<Predicate['mode']>
): Modifier<[], 'predicate'> {
    return createModifier('predicate', ++predicateId, [], {
        ...predicate.predicate!,
        mode,
    });
}

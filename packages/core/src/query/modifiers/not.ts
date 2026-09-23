import { $internal } from '../../common';
import type { Predicate } from '../../predicate/types';
import { isPredicate } from '../../predicate/utils/is-predicate';
import type { ExtractTraits, Trait } from '../../trait/types';
import type { Modifier } from '../types';
import { createModifier } from '../modifier';

export const Not = <T extends (Trait | Predicate)[] = Trait[]>(
    ...inputs: T
): Modifier<ExtractTraits<T>, 'not'> => {
    const traits = inputs.map((input) =>
        isPredicate(input) ? input[$internal].trait : input
    ) as ExtractTraits<T>;
    return createModifier('not', 1, traits);
};

import type { Aspect } from '../../aspect/types';
import type { ExtractModifierTraits, Trait } from '../../trait/types';
import type { Modifier } from '../types';
import { createAspectAwareModifier } from '../modifier';

export const Not = <T extends (Trait | Aspect)[] = Trait[]>(
    ...traits: T
): Modifier<ExtractModifierTraits<T>, 'not'> => {
    return createAspectAwareModifier('not', 1, traits) as Modifier<ExtractModifierTraits<T>, 'not'>;
};

import { isAspect } from '../../aspect/is-aspect';
import type { Aspect } from '../../aspect/types';
import { $internal } from '../../common';
import type { Trait } from '../../trait/types';
import { $modifier, createModifier } from '../modifier';
import type { Modifier, OrModifier, OrParameter } from '../types';

export const Or = <T extends OrParameter[]>(...params: T): OrModifier<T> => {
    // Separate traits from nested modifiers. Aspects stand in for their completeness trait.
    const traits: Trait[] = [];
    const sources: (Trait | Aspect)[] = [];
    const modifiers: Modifier[] = [];

    for (const param of params) {
        if ((param as Modifier)[$modifier]) {
            modifiers.push(param as Modifier);
        } else if (isAspect(param)) {
            traits.push(param[$internal].completeness);
            sources.push(param);
        } else {
            traits.push(param as Trait);
            sources.push(param as Trait);
        }
    }

    const modifier = createModifier('or', 2, traits, sources) as OrModifier<T>;
    modifier.modifiers = modifiers;

    return modifier;
};

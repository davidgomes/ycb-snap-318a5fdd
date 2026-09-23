import { isAspect } from '../../aspect/aspect';
import type { Aspect, ModifierTerm } from '../../aspect/aspect';
import type { Trait } from '../../trait/types';
import { $modifier, createModifier } from '../modifier';
import type { Modifier, OrModifier, OrParameter } from '../types';

export const Or = <T extends OrParameter[]>(...params: T): OrModifier<T> => {
    // Separate traits from nested modifiers and aspect groups.
    const traits: Trait[] = [];
    const modifiers: Modifier[] = [];
    const aspects: Aspect[] = [];
    const terms: ModifierTerm[] = [];

    for (const param of params) {
        if ((param as Modifier)[$modifier]) {
            modifiers.push(param as Modifier);
        } else if (isAspect(param)) {
            aspects.push(param);
            terms.push({ kind: 'aspect', aspect: param });
            for (let i = 0; i < param.traits.length; i++) traits.push(param.traits[i]);
        } else {
            traits.push(param as Trait);
            terms.push({ kind: 'trait', trait: param as Trait });
        }
    }

    const modifier = createModifier('or', 2, traits, aspects, terms) as OrModifier<T>;
    modifier.modifiers = modifiers;

    return modifier;
};

import { isAspect } from '../../aspect/aspect';
import type { Trait } from '../../trait/types';
import { $modifier, createModifier } from '../modifier';
import type { Aspect } from '../../aspect/types';
import type { PartsOf } from '../modifier-inputs';
import type { AspectGroup, Modifier, ModifierPart, OrModifier, OrParameter } from '../types';

type OrResult<T extends readonly OrParameter[]> = Extract<T[number], Aspect> extends never
    ? OrModifier<T>
    : OrModifier<T> & { parts: PartsOf<T> };

export const Or = <T extends OrParameter[]>(...params: T): OrResult<T> => {
    // Separate traits from nested modifiers and aspects.
    const traits: Trait[] = [];
    const modifiers: Modifier[] = [];
    const aspectGroups: AspectGroup[] = [];
    const parts: ModifierPart[] = [];
    let hasAspect = false;

    for (const param of params) {
        if ((param as Modifier)[$modifier]) {
            modifiers.push(param as Modifier);
        } else if (isAspect(param)) {
            hasAspect = true;
            aspectGroups.push({
                aspectId: param.id,
                traits: param.traits,
                traitIds: param.traits.map((trait) => trait.id),
            });
            parts.push({ kind: 'aspect', aspectId: param.id, traits: param.traits });
        } else {
            const trait = param as Trait;
            traits.push(trait);
            parts.push({ kind: 'trait', trait });
        }
    }

    const modifier = createModifier(
        'or',
        2,
        traits,
        hasAspect ? { aspectGroups, parts } : undefined
    ) as OrResult<T>;
    modifier.modifiers = modifiers;

    return modifier;
};

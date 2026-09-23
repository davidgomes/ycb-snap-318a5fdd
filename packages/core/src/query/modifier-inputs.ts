import { $internal } from '../common';
import { isAspect } from '../aspect/aspect';
import type { Aspect } from '../aspect/types';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import type { Trait } from '../trait/types';
import type { Modifier, ModifierPart } from './types';

type ContainsAspect<T extends readonly unknown[]> = Extract<T[number], Aspect> extends never
    ? false
    : true;

type ToPart<T> = T extends Aspect<infer Traits>
    ? { kind: 'aspect'; aspectId: number; traits: Traits }
    : T extends Relation<infer R>
      ? { kind: 'trait'; trait: R }
      : T extends Trait
        ? { kind: 'trait'; trait: T }
        : never;

export type PartsOf<T extends readonly unknown[]> = T extends readonly [infer Head, ...infer Tail]
        ? ToPart<Head> extends never
        ? Tail extends readonly unknown[]
            ? PartsOf<Tail>
            : []
        : [ToPart<Head>, ...(Tail extends readonly unknown[] ? PartsOf<Tail> : [])]
    : [];

type WritableTraits<T> = T extends readonly [infer Head, ...infer Tail]
    ? Head extends Relation<infer R>
        ? [R, ...WritableTraits<Tail>]
        : Head extends Trait
          ? [Head, ...WritableTraits<Tail>]
          : WritableTraits<Tail>
    : [];

/** Preserve constituent trait types unless an aspect is present, in which case `parts` carries order. */
export type ModifierFromInputs<T extends readonly unknown[], TType extends string> =
    ContainsAspect<T> extends true
        ? Modifier<Trait[], TType> & { parts: PartsOf<T> }
        : Modifier<WritableTraits<T> extends Trait[] ? WritableTraits<T> : never, TType>;

export type CollectedModifierInputs = {
    traits: Trait[];
    aspectGroups: { aspectId: number; traits: readonly Trait[]; traitIds: number[] }[];
    parts?: readonly ModifierPart[];
};

/** Split modifier arguments into plain traits and aspect groups, preserving order. */
export function collectModifierInputs(inputs: readonly unknown[]): CollectedModifierInputs {
    const traits: Trait[] = [];
    const aspectGroups: CollectedModifierInputs['aspectGroups'] = [];
    const parts: ModifierPart[] = [];
    let hasAspect = false;

    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];
        if (isRelation(input)) {
            const trait = input[$internal].trait;
            traits.push(trait);
            parts.push({ kind: 'trait', trait });
        } else if (isAspect(input)) {
            hasAspect = true;
            const group = {
                aspectId: input.id,
                traits: input.traits,
                traitIds: input.traits.map((trait) => trait.id),
            };
            aspectGroups.push(group);
            parts.push({ kind: 'aspect', aspectId: input.id, traits: input.traits });
        } else {
            const trait = input as Trait;
            traits.push(trait);
            parts.push({ kind: 'trait', trait });
        }
    }

    return {
        traits,
        aspectGroups,
        parts: hasAspect ? parts : undefined,
    };
}

export function modifierExtras(
    collected: CollectedModifierInputs
): Pick<Modifier, 'aspectGroups' | 'parts'> | undefined {
    if (!collected.parts) return undefined;
    return {
        aspectGroups: collected.aspectGroups,
        parts: collected.parts,
    };
}

export type { Aspect };

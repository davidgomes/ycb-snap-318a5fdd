import { $internal, Brand } from '../common';
import type { RelationPair } from '../relation/types';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import { ExtractTraits, Trait, TrackingModifierInput } from '../trait/types';
import { EventType, Modifier, OrModifier, QueryParameter } from './types';

export const $modifier = Symbol('modifier');

export function createModifier<TTrait extends Trait[] = Trait[], TType extends string = string>(
    type: TType,
    id: number,
    traits: TTrait,
    pairs?: (RelationPair | undefined)[]
): Modifier<TTrait, TType> {
    const modifier: Modifier<TTrait, TType> = {
        [$modifier]: true,
        type,
        id,
        traits,
        traitIds: traits.map((trait) => trait.id),
    };
    if (pairs) modifier.pairs = pairs;
    return modifier;
}

/** Create a tracking modifier from traits, relations (tracked as a whole) and relation pairs (tracked per target) */
export function createTrackingModifier<T extends TrackingModifierInput[], TType extends string>(
    type: TType,
    id: number,
    inputs: T
): Modifier<ExtractTraits<T>, TType> {
    const traits: Trait[] = [];
    let pairs: (RelationPair | undefined)[] | undefined;

    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];

        if (isRelationPair(input)) {
            traits.push(input[$internal].relation[$internal].trait);
            pairs ??= Array.from({ length: inputs.length });
            pairs[i] = input;
        } else if (isRelation(input)) {
            traits.push(input[$internal].trait);
        } else {
            traits.push(input);
        }
    }

    return createModifier(type, id, traits as ExtractTraits<T>, pairs);
}

export /* @inline @pure */ function isModifier(param: QueryParameter): param is Modifier {
    return (param as Brand<typeof $modifier> | null | undefined)?.[$modifier] as unknown as boolean;
}

/** Check if a modifier is a tracking modifier (added, removed, or changed) */
export function isTrackingModifier(modifier: Modifier): boolean {
    const { type } = modifier;
    return type.includes('added') || type.includes('removed') || type.includes('changed');
}

/** Get the tracking type from a modifier */
export function getTrackingType(modifier: Modifier): EventType | null {
    const { type } = modifier;
    if (type.includes('added')) return 'add';
    if (type.includes('removed')) return 'remove';
    if (type.includes('changed')) return 'change';
    return null;
}

/** Check if an Or modifier has nested modifiers */
export function isOrWithModifiers(modifier: Modifier): modifier is OrModifier {
    return modifier.type === 'or' && Array.isArray((modifier as OrModifier).modifiers);
}

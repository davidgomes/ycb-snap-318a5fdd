import { $internal, Brand } from '../common';
import type { RelationPair } from '../relation/types';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import type { Trait, TraitOrRelation } from '../trait/types';
import { EventType, Modifier, OrModifier, QueryParameter } from './types';

export const $modifier = Symbol('modifier');

export function createModifier<TTrait extends Trait[] = Trait[], TType extends string = string>(
    type: TType,
    id: number,
    traits: TTrait
): Modifier<TTrait, TType> {
    return {
        [$modifier]: true,
        type,
        id,
        traits,
        traitIds: traits.map((trait) => trait.id),
    } as const;
}

/** Create a tracking modifier from traits, relations, or relation pairs */
export function createTrackingModifier<TType extends string>(
    type: TType,
    id: number,
    inputs: (TraitOrRelation | RelationPair)[]
): Modifier<Trait[], TType> {
    const traits: Trait[] = [];
    const pairs: RelationPair[] = [];

    for (const input of inputs) {
        if (isRelationPair(input)) pairs.push(input);
        else if (isRelation(input)) traits.push(input[$internal].trait);
        else traits.push(input as Trait);
    }

    const modifier = createModifier(type, id, traits);
    if (pairs.length > 0) modifier.pairs = pairs;
    return modifier;
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

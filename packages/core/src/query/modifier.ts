import { Brand } from '../common';
import type { RelationTarget } from '../relation/types';
import { Trait } from '../trait/types';
import { EventType, Modifier, OrModifier, QueryParameter } from './types';

export const $modifier = Symbol('modifier');

export function createModifier<TTrait extends Trait[] = Trait[], TType extends string = string>(
    type: TType,
    id: number,
    traits: TTrait,
    pairTargets?: readonly (RelationTarget | undefined)[]
): Modifier<TTrait, TType> {
    const modifier: Modifier<TTrait, TType> = {
        [$modifier]: true,
        type,
        id,
        traits,
        traitIds: traits.map((trait) => trait.id),
    };

    if (pairTargets?.some((target) => target !== undefined)) {
        modifier.pairTargets = pairTargets;
    }

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

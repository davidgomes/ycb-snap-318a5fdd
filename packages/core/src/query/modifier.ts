import { isAspect } from '../aspect/aspect';
import type { Aspect } from '../aspect/types';
import { $internal, Brand } from '../common';
import { Trait } from '../trait/types';
import { EventType, Modifier, OrModifier, QueryParameter } from './types';

export const $modifier = Symbol('modifier');

export function createModifier<
    TTrait extends (Trait | Aspect)[] = Trait[],
    TType extends string = string,
>(type: TType, id: number, traits: TTrait): Modifier<TTrait, TType> {
    return {
        [$modifier]: true,
        type,
        id,
        traits,
        traitIds: traits.map((trait) => trait.id),
    } as Modifier<TTrait, TType>;
}

/** Create a modifier from inputs that may include aspects. Aspects are represented by their marker. */
export function createAspectAwareModifier<TType extends string>(
    type: TType,
    id: number,
    inputs: (Trait | Aspect)[]
): Modifier<Trait[], TType> {
    const traits: Trait[] = [];
    let aspects: Aspect[] | undefined;
    for (const input of inputs) {
        if (isAspect(input)) {
            (aspects ??= []).push(input);
            traits.push(input[$internal].marker);
        } else {
            traits.push(input);
        }
    }
    const modifier = createModifier(type, id, traits) as Modifier<Trait[], TType>;
    if (aspects) modifier.aspects = aspects;
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

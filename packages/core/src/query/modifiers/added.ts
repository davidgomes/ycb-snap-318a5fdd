import { $internal } from '../../common';
import { isRelation } from '../../relation/utils/is-relation';
import type { ExtractTraits, Trait, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier, isPredicate } from '../modifier';
import type { Modifier, Predicate } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';

export function createAdded() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    function added<T extends TraitOrRelation[]>(
        ...inputs: T
    ): Modifier<ExtractTraits<T>, `added-${number}`>;
    function added(predicate: Predicate): Modifier<[], `added-${number}`>;
    function added(
        ...inputs: Array<TraitOrRelation | Predicate>
    ): Modifier<any, `added-${number}`> {
        const { traits, predicates } = splitTrackedInputs(inputs);
        const modifier = createModifier(`added-${id}`, id, traits);
        if (predicates.length) modifier.predicates = predicates;
        return modifier;
    }

    return added;
}

export function splitTrackedInputs(inputs: readonly (TraitOrRelation | Predicate)[]) {
    const traits: Trait[] = [];
    const predicates: Predicate[] = [];

    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];
        if (isPredicate(input)) predicates.push(input);
        else if (isRelation(input)) traits.push(input[$internal].trait);
        else traits.push(input);
    }

    return { traits, predicates };
}

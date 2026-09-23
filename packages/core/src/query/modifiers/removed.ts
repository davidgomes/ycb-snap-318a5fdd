import { $internal } from '../../common';
import { isRelation } from '../../relation/utils/is-relation';
import type { ExtractTraits, Trait, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import { isPredicate, type Predicate } from '../predicate';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';

export function createRemoved() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    function removed<T extends TraitOrRelation[]>(
        ...inputs: T
    ): Modifier<ExtractTraits<T>, `removed-${number}`>;
    function removed(...inputs: Array<TraitOrRelation | Predicate>): Modifier;
    function removed(...inputs: Array<TraitOrRelation | Predicate>): Modifier {
        const traits: Trait[] = [];
        const predicates: Predicate[] = [];

        for (let i = 0; i < inputs.length; i++) {
            const input = inputs[i];
            if (isPredicate(input)) predicates.push(input);
            else traits.push(isRelation(input) ? input[$internal].trait : input);
        }

        const modifier = createModifier(`removed-${id}`, id, traits);
        modifier.predicates = predicates;
        return modifier;
    }

    return removed;
}

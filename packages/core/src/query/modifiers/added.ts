import { $internal } from '../../common';
import { isRelation } from '../../relation/utils/is-relation';
import type { ExtractTraits, Trait, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import { isPredicate } from '../predicate';
import type { Modifier, Predicate } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';

export function createAdded() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends TraitOrRelation[]>(
        ...inputs: [...T, ...Predicate[]]
    ): Modifier<ExtractTraits<T>, `added-${number}`> => {
        const traits: Trait[] = [];
        const predicates: Predicate[] = [];
        for (let i = 0; i < inputs.length; i++) {
            const input = inputs[i] as TraitOrRelation | Predicate;
            if (isPredicate(input)) predicates.push(input);
            else if (isRelation(input)) traits.push(input[$internal].trait);
            else traits.push(input);
        }
        const modifier = createModifier(`added-${id}`, id, traits as ExtractTraits<T>);
        if (predicates.length) modifier.predicates = predicates;
        return modifier;
    };
}

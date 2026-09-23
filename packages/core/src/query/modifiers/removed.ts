import type { ExtractTraits, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import { splitModifierInputs } from '../predicate';
import type { Modifier, Predicate } from '../types';
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
    function removed(...inputs: Predicate[]): Modifier<[], `removed-${number}`>;
    function removed(...inputs: Array<TraitOrRelation | Predicate>): Modifier;
    function removed(...inputs: Array<TraitOrRelation | Predicate>): Modifier {
        const { traits, predicates } = splitModifierInputs(inputs, true);
        const modifier = createModifier(`removed-${id}`, id, traits);
        if (predicates.length > 0) modifier.predicates = predicates;
        return modifier;
    }

    return removed;
}

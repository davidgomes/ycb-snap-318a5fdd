import type { ExtractTraits, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import type { Modifier, Predicate } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';
import { splitTrackedInputs } from './added';

export function createRemoved() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    function removed<T extends TraitOrRelation[]>(
        ...inputs: T
    ): Modifier<ExtractTraits<T>, `removed-${number}`>;
    function removed(predicate: Predicate): Modifier<[], `removed-${number}`>;
    function removed(
        ...inputs: Array<TraitOrRelation | Predicate>
    ): Modifier<any, `removed-${number}`> {
        const { traits, predicates } = splitTrackedInputs(inputs);
        const modifier = createModifier(`removed-${id}`, id, traits);
        if (predicates.length) modifier.predicates = predicates;
        return modifier;
    }

    return removed;
}

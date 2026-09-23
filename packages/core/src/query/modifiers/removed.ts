import { $internal } from '../../common';
import { isRelation } from '../../relation/utils/is-relation';
import type { ExtractTraits, Trait, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import { isPredicate, type Predicate, wrapPredicate } from '../predicate';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';

export function createRemoved() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    function modifier<T extends Trait[]>(predicate: Predicate<T>): Predicate<T, 'removed'>;
    function modifier<T extends TraitOrRelation[]>(
        ...inputs: T
    ): Modifier<ExtractTraits<T>, `removed-${number}`>;
    function modifier(...inputs: any[]): any {
        if (inputs.length === 1 && isPredicate(inputs[0])) {
            return wrapPredicate(inputs[0], 'removed', id);
        }
        const traits = inputs.map((input) =>
            isRelation(input) ? input[$internal].trait : input
        ) as Trait[];
        return createModifier(`removed-${id}`, id, traits);
    }

    return modifier;
}

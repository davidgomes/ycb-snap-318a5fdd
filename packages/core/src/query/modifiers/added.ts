import { $internal } from '../../common';
import { isRelation } from '../../relation/utils/is-relation';
import type { ExtractTraits, Trait, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import { isPredicate, type Predicate, wrapPredicate } from '../predicate';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';

export function createAdded() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    function modifier<T extends Trait[]>(predicate: Predicate<T>): Predicate<T, 'added'>;
    function modifier<T extends TraitOrRelation[]>(
        ...inputs: T
    ): Modifier<ExtractTraits<T>, `added-${number}`>;
    function modifier(...inputs: any[]): any {
        if (inputs.length === 1 && isPredicate(inputs[0])) {
            return wrapPredicate(inputs[0], 'added', id);
        }
        const traits = inputs.map((input) =>
            isRelation(input) ? input[$internal].trait : input
        ) as Trait[];
        return createModifier(`added-${id}`, id, traits);
    }

    return modifier;
}

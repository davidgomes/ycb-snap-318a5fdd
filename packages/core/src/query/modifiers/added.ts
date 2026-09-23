import { splitModifierInputs } from '../../aspect/split-modifier-inputs';
import type { Aspect, NormalizeModifierSources } from '../../aspect/types';
import type { Trait, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';

export function createAdded() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends readonly (TraitOrRelation | Aspect)[]>(
        ...inputs: T
    ): Modifier<Trait[], `added-${number}`, NormalizeModifierSources<T>> => {
        const { traits, sources } = splitModifierInputs(inputs);
        return createModifier(`added-${id}`, id, traits, sources as NormalizeModifierSources<T>);
    };
}

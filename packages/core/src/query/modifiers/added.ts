import { $internal } from '../../common';
import { isRelation } from '../../relation/utils/is-relation';
import type { ExtractTraits, TraitOrRelation } from '../../trait/types';
import { universe } from '../../universe/universe';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';
import { isPredicateModifier, type AnyPredicateModifier } from './predicate';

function splitTrackingInputs(inputs: (TraitOrRelation | AnyPredicateModifier)[]) {
    const traits: Trait[] = [];
    const predicates: AnyPredicateModifier[] = [];

    for (const input of inputs) {
        if (isPredicateModifier(input)) predicates.push(input);
        else traits.push(isRelation(input) ? input[$internal].trait : input);
    }

    return { traits: traits as ExtractTraits<TraitOrRelation[]>, predicates };
}

import type { Trait } from '../../trait/types';

export function createAdded() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return (...inputs: (TraitOrRelation | AnyPredicateModifier)[]) => {
        const { traits, predicates } = splitTrackingInputs(inputs);
        const modifier = createModifier(`added-${id}`, id, traits);
        if (predicates.length > 0) modifier.predicates = predicates;
        return modifier;
    };
}

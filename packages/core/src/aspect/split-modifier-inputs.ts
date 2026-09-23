import { $internal } from '../common';
import { isRelation } from '../relation/utils/is-relation';
import type { Trait } from '../trait/types';
import { isAspect } from './is-aspect';
import type { Aspect } from './types';

export function splitModifierInputs(inputs: readonly unknown[]): {
    traits: Trait[];
    sources: (Trait | Aspect)[];
} {
    const traits: Trait[] = [];
    const sources: (Trait | Aspect)[] = [];

    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];
        if (isAspect(input)) {
            sources.push(input);
            traits.push(input[$internal].completeness);
            continue;
        }
        if (isRelation(input)) {
            const trait = input[$internal].trait;
            sources.push(trait);
            traits.push(trait);
            continue;
        }
        const trait = input as Trait;
        sources.push(trait);
        traits.push(trait);
    }

    return { traits, sources };
}

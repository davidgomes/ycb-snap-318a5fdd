import { $internal } from '../../common';
import { isRelation } from '../../relation/utils/is-relation';
import type { Trait } from '../../trait/types';
import type { AspectInput } from '../types';
import { isAspect } from './is-aspect';

export function flattenAspectConstituents(inputs: AspectInput[]): Trait[] {
    const traits: Trait[] = [];
    const seen = new Set<number>();

    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];

        if (isAspect(input)) {
            const nested = input.traits;
            for (let j = 0; j < nested.length; j++) {
                const trait = nested[j];
                if (!seen.has(trait.id)) {
                    seen.add(trait.id);
                    traits.push(trait);
                }
            }
            continue;
        }

        if (isRelation(input)) {
            throw new Error('Koota: relation constituents are not supported in aspects.');
        }

        const trait = input as Trait;
        if (trait[$internal].relation) {
            throw new Error('Koota: relation constituents are not supported in aspects.');
        }

        if (!seen.has(trait.id)) {
            seen.add(trait.id);
            traits.push(trait);
        }
    }

    return traits;
}

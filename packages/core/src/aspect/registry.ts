import type { Trait } from '../trait/types';
import type { Aspect } from './types';

const traitAspects = new Map<Trait, Aspect[]>();

export function registerAspect(aspect: Aspect) {
    for (const t of aspect.traits) {
        let list = traitAspects.get(t);
        if (!list) traitAspects.set(t, (list = []));
        list.push(aspect);
    }
}

export /* @inline @pure */ function getTraitAspects(t: Trait): Aspect[] | undefined {
    return traitAspects.get(t);
}

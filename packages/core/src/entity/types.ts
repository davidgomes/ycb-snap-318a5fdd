import type { Aspect, AspectInit, GetValue, SetValue } from '../aspect/types';
import type { Relation, RelationPair } from '../relation/types';
import type { ConfigurableTrait, Trait } from '../trait/types';

export type Entity = number & {
    add: (...traits: (ConfigurableTrait | Aspect | AspectInit)[]) => void;
    remove: (...traits: (Trait | RelationPair | Aspect)[]) => void;
    has: (trait: Trait | RelationPair | Aspect) => boolean;
    destroy: () => void;
    changed: (trait: Trait) => void;
    set: <T extends Trait | RelationPair | Aspect>(
        trait: T,
        value: SetValue<T>,
        flagChanged?: boolean
    ) => void;
    get: <T extends Trait | RelationPair | Aspect>(trait: T) => GetValue<T>;
    targetFor: <T extends Trait>(relation: Relation<T>) => Entity | undefined;
    targetsFor: <T extends Trait>(relation: Relation<T>) => Entity[];
    id: () => number;
    generation: () => number;
    isAlive: () => boolean;
};

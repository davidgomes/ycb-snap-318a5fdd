import type { Aspect, AspectRecord, ConfigurableAspect } from '../aspect/types';
import type { Relation, RelationPair } from '../relation/types';
import type {
    ConfigurableTrait,
    ExtractSchema,
    SetTraitCallback,
    Trait,
    TraitRecord,
    TraitValue,
} from '../trait/types';

export type Entity = number & {
    add: (...traits: (ConfigurableTrait | ConfigurableAspect)[]) => void;
    remove: (...traits: (Trait | RelationPair | Aspect)[]) => void;
    has: (trait: Trait | RelationPair | Aspect) => boolean;
    destroy: () => void;
    changed: (trait: Trait) => void;
    set: <T extends Trait | RelationPair | Aspect>(
        trait: T,
        value: T extends Aspect
            ? AspectRecord<T> | ((prev: AspectRecord<T> | undefined) => AspectRecord<T>)
            : TraitValue<ExtractSchema<T>> | SetTraitCallback<T>,
        flagChanged?: boolean
    ) => void;
    get: <T extends Trait | RelationPair | Aspect>(
        trait: T
    ) => T extends Aspect ? AspectRecord<T> | undefined : TraitRecord<ExtractSchema<T>> | undefined;
    targetFor: <T extends Trait>(relation: Relation<T>) => Entity | undefined;
    targetsFor: <T extends Trait>(relation: Relation<T>) => Entity[];
    id: () => number;
    generation: () => number;
    isAlive: () => boolean;
};

import type { Aspect, AspectRecord, AspectValue, SetAspectCallback } from '../aspect/types';
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
    add: (...traits: ConfigurableTrait[]) => void;
    remove: (...traits: (Trait | RelationPair | Aspect)[]) => void;
    has: (trait: Trait | RelationPair | Aspect) => boolean;
    destroy: () => void;
    changed: (trait: Trait) => void;
    set: {
        <T extends Trait | RelationPair>(
            trait: T,
            value: TraitValue<ExtractSchema<T>> | SetTraitCallback<T>,
            flagChanged?: boolean
        ): void;
        <A extends Aspect>(
            aspect: A,
            value: AspectValue<A> | SetAspectCallback<A>,
            flagChanged?: boolean
        ): void;
    };
    get: {
        <T extends Trait | RelationPair>(trait: T): TraitRecord<ExtractSchema<T>> | undefined;
        <A extends Aspect>(aspect: A): AspectRecord<A> | undefined;
    };
    targetFor: <T extends Trait>(relation: Relation<T>) => Entity | undefined;
    targetsFor: <T extends Trait>(relation: Relation<T>) => Entity[];
    id: () => number;
    generation: () => number;
    isAlive: () => boolean;
};

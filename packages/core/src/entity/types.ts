import type { Aspect, AspectRecord, AspectValue } from '../aspect/types';
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
    set: (<A extends Aspect>(
        aspect: A,
        value: AspectValue<A> | ((prev: AspectRecord<A>) => AspectValue<A>),
        flagChanged?: boolean
    ) => void) &
        (<T extends Trait | RelationPair>(
            trait: T,
            value: TraitValue<ExtractSchema<T>> | SetTraitCallback<T>,
            flagChanged?: boolean
        ) => void);
    get: (<A extends Aspect>(aspect: A) => AspectRecord<A> | undefined) &
        (<T extends Trait | RelationPair>(trait: T) => TraitRecord<ExtractSchema<T>> | undefined);
    targetFor: <T extends Trait>(relation: Relation<T>) => Entity | undefined;
    targetsFor: <T extends Trait>(relation: Relation<T>) => Entity[];
    id: () => number;
    generation: () => number;
    isAlive: () => boolean;
};

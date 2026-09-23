import type { Aspect, MergeTraits } from '../aspect/types';
import type { Relation, RelationPair } from '../relation/types';
import type {
    ConfigurableTrait,
    ExtractSchema,
    SetTraitCallback,
    Trait,
    TraitRecord,
    TraitValue,
} from '../trait/types';

type AspectValue<T extends Aspect> = Partial<MergeTraits<T['traits']>>;

export type Entity = number & {
    add: (
        ...traits: Array<ConfigurableTrait | Aspect | [Aspect, AspectValue<Aspect>]>
    ) => void;
    remove: (...traits: Array<Trait | RelationPair | Aspect>) => void;
    has: (trait: Trait | RelationPair | Aspect) => boolean;
    destroy: () => void;
    changed: (trait: Trait) => void;
    set: {
        <T extends Trait | RelationPair>(
            trait: T,
            value: TraitValue<ExtractSchema<T>> | SetTraitCallback<T>,
            flagChanged?: boolean
        ): void;
        <T extends Aspect>(
            aspect: T,
            value: AspectValue<T> | ((prev: MergeTraits<T['traits']>) => AspectValue<T>)
        ): void;
    };
    get: {
        <T extends Trait | RelationPair>(trait: T): TraitRecord<ExtractSchema<T>> | undefined;
        <T extends Aspect>(aspect: T): MergeTraits<T['traits']> | undefined;
    };
    targetFor: <T extends Trait>(relation: Relation<T>) => Entity | undefined;
    targetsFor: <T extends Trait>(relation: Relation<T>) => Entity[];
    id: () => number;
    generation: () => number;
    isAlive: () => boolean;
};

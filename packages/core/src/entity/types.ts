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
    remove: (...traits: (Trait | RelationPair | Aspect<any>)[]) => void;
    has: (trait: Trait | RelationPair | Aspect<any>) => boolean;
    destroy: () => void;
    changed: (trait: Trait | Aspect<any>) => void;
    set: <T extends Trait | RelationPair | Aspect<any>>(
        trait: T,
        value: T extends Aspect<any>
            ? AspectValue<T> | SetAspectCallback<T>
            : T extends Trait | RelationPair
              ? TraitValue<ExtractSchema<T>> | SetTraitCallback<T>
              : never,
        flagChanged?: boolean
    ) => void;
    get: <T extends Trait | RelationPair | Aspect<any>>(
        trait: T
    ) =>
        | (T extends Aspect<any>
              ? AspectRecord<T>
              : T extends Trait | RelationPair
                ? TraitRecord<ExtractSchema<T>>
                : never)
        | undefined;
    targetFor: <T extends Trait>(relation: Relation<T>) => Entity | undefined;
    targetsFor: <T extends Trait>(relation: Relation<T>) => Entity[];
    id: () => number;
    generation: () => number;
    isAlive: () => boolean;
};

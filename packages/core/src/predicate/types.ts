import type { $internal } from '../common';
import type { Entity } from '../entity/types';
import type { TagTrait, Trait, TraitInstance, TraitRecord } from '../trait/types';
import type { $predicate } from './symbols';

/** The data of each dependency trait, in the order the dependencies were declared */
export type PredicateValues<T extends Trait[]> = {
    [K in keyof T]: T[K] extends Trait ? TraitRecord<T[K]> : never;
};

export type Predicate<T extends Trait[] = Trait[]> = {
    readonly [$predicate]: true;
    readonly [$internal]: {
        /** Tag trait kept on every entity the predicate currently matches */
        trait: TagTrait;
        dependencies: T;
        fn: (values: unknown[]) => boolean;
    };
};

export type PredicateInstance = {
    predicate: Predicate;
    trait: TraitInstance;
    dependencies: TraitInstance[];
    /** Entities waiting to be re-evaluated once deferral ends */
    pending: Set<Entity>;
};

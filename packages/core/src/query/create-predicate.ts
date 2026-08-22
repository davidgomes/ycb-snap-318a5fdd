import { $internal } from '../common';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import { trait } from '../trait/trait';
import type { TagTrait, Trait, TraitRecord } from '../trait/types';
import {
    evaluatePredicateOnAllWorlds,
    registerPredicateDefinition,
    type PredicateDefinition,
} from './utils/predicate-runtime';

export type TraitRecordsFromTraits<T extends readonly Trait[]> = {
    [K in keyof T]: TraitRecord<T[K]>;
};

export type Predicate = TagTrait;

export function createPredicate<const T extends readonly Trait[]>(
    traits: T,
    predicate: (data: TraitRecordsFromTraits<T>) => boolean
): Predicate {
    if (traits.length === 0) {
        throw new Error('Koota: Predicates require at least one dependency trait.');
    }

    for (let i = 0; i < traits.length; i++) {
        const dependency = traits[i];
        if (isRelation(dependency) || isRelationPair(dependency) || dependency[$internal].relation) {
            throw new Error('Koota: Predicates cannot depend on relations.');
        }
        if (dependency[$internal].type === 'tag') {
            throw new Error('Koota: Predicates cannot depend on tag traits.');
        }
    }

    const result = trait();
    result[$internal].isPredicate = true;

    const definition: PredicateDefinition = {
        trait: result,
        dependencies: traits as unknown as Trait[],
        evaluate: predicate as (data: TraitRecord<Trait>[]) => boolean,
        scratch: new Array(traits.length),
    };

    registerPredicateDefinition(definition);
    evaluatePredicateOnAllWorlds(definition);

    return result;
}

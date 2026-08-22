import { $internal } from '../common';
import { isRelation } from '../relation/utils/is-relation';
import type { Trait } from '../trait/types';
import { $modifier, createModifier } from './modifier';
import type { Modifier } from './types';

type Predicate = Modifier & { predicate: (data: unknown[]) => boolean };

export function createPredicate<T extends Trait[]>(
    dependencies: T,
    predicate: (data: { [K in keyof T]: T[K] extends Trait ? unknown : never }) => boolean
): Predicate {
    if (dependencies.some((dependency) => isRelation(dependency))) {
        throw new Error('Relations cannot be predicate dependencies');
    }
    if (dependencies.some((dependency) => dependency[$internal].type === 'tag')) {
        throw new Error('Tags cannot be predicate dependencies');
    }
    const modifier = createModifier('predicate', 0, dependencies);
    modifier.predicate = predicate as (data: unknown[]) => boolean;
    modifier.predicateMode = 'current';
    return modifier;
}

export function isPredicate(value: unknown): value is Predicate {
    return !!value && typeof value === 'object' && $modifier in value && 'predicate' in value;
}

export function wrapPredicate(
    modifier: Predicate,
    predicate: (data: unknown[]) => boolean,
    mode: Predicate['predicateMode'] = 'current'
): Predicate {
    const wrapped = createModifier('predicate', modifier.id, modifier.traits) as Predicate;
    wrapped.predicate = predicate;
    wrapped.predicateMode = mode;
    return wrapped;
}

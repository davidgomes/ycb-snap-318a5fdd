import { $internal } from '../../common';
import { isRelation } from '../../relation/utils/is-relation';
import type { Trait, TraitRecord } from '../../trait/types';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';
import { universe } from '../../universe/universe';
import { $modifier, createModifier } from '../modifier';
import type { Modifier } from '../types';

export const $predicate = Symbol('predicate');

export type PredicateValues<T extends Trait[]> = {
    [K in keyof T]: T[K] extends Trait ? TraitRecord<T[K]> : never;
};

export type PredicateModifier<T extends Trait[] = Trait[]> = Modifier<T, `predicate-${number}`> & {
    [$predicate]: true;
    predicate: (values: PredicateValues<T>) => boolean;
};

let predicateId = 0;

export function isPredicateModifier(param: unknown): param is PredicateModifier {
    return (
        typeof param === 'object' &&
        param !== null &&
        ($predicate in param || (param as Modifier).type?.startsWith('predicate-'))
    );
}

export function createPredicate<T extends Trait[]>(
    dependencies: [...T],
    predicate: (values: PredicateValues<T>) => boolean
): PredicateModifier<T> {
    for (const dep of dependencies) {
        if (dep[$internal].type === 'tag') {
            throw new Error('Tags cannot be used as predicate dependencies');
        }
        if (isRelation(dep)) {
            throw new Error('Relations cannot be used as predicate dependencies');
        }
    }

    const id = predicateId++;
    const modifier = createModifier(`predicate-${id}`, id, dependencies) as PredicateModifier<T>;
    modifier[$predicate] = true;
    modifier.predicate = predicate as (values: PredicateValues<T>) => boolean;

    return modifier;
}

export function createPredicateTrackingId() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return id;
}

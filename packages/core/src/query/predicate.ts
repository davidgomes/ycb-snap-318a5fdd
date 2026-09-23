import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { isRelation } from '../relation/utils/is-relation';
import { getTrait, hasTrait } from '../trait/trait';
import type { Trait, TraitRecord } from '../trait/types';
import type { World } from '../world';
import { createModifier, isModifier } from './modifier';
import type { Modifier, QueryParameter } from './types';

export const $predicate = Symbol('predicate');

export type PredicateKind = 'is' | 'not' | 'added' | 'removed' | 'changed';

type TraitDataTuple<T extends Trait[]> = { [K in keyof T]: TraitRecord<T[K]> };

export type Predicate<T extends Trait[] = Trait[], TKind extends PredicateKind = PredicateKind> =
    Modifier<[], `predicate-${TKind}`> & {
        [$predicate]: {
            kind: TKind;
            base: number;
            deps: T;
            fn: (data: TraitDataTuple<T>) => unknown;
        };
    };

let predicateCount = 0;

// Keeps predicate ids clear of regular modifier ids in the query hash.
const PREDICATE_ID_BASE = 1_000_000;
const KIND_INDEX: Record<PredicateKind, number> = {
    is: 0,
    not: 1,
    added: 2,
    removed: 3,
    changed: 4,
};

function buildPredicate<T extends Trait[], TKind extends PredicateKind>(
    kind: TKind,
    base: number,
    deps: T,
    fn: (data: TraitDataTuple<T>) => unknown,
    trackingId = 0
): Predicate<T, TKind> {
    const id = PREDICATE_ID_BASE + base * 8192 + KIND_INDEX[kind] * 1024 + (trackingId % 1024);
    const modifier = createModifier(`predicate-${kind}`, id, []) as Predicate<T, TKind>;
    modifier[$predicate] = { kind, base, deps, fn };
    return modifier;
}

export function createPredicate<T extends Trait[]>(
    traits: [...T],
    fn: (data: TraitDataTuple<T>) => unknown
): Predicate<T, 'is'> {
    for (const trait of traits) {
        if (isRelation(trait)) throw new Error('Koota: Relations cannot be predicate dependencies.');
        if (trait[$internal].type === 'tag') {
            throw new Error('Koota: Tags cannot be predicate dependencies.');
        }
    }
    return buildPredicate('is', predicateCount++, traits as T, fn);
}

export function isPredicate(param: unknown): param is Predicate {
    return isModifier(param as QueryParameter) && $predicate in (param as object);
}

export function wrapPredicate<TKind extends PredicateKind>(
    predicate: Predicate,
    kind: TKind,
    trackingId = 0
): Predicate<Trait[], TKind> {
    const { base, deps, fn } = predicate[$predicate];
    return buildPredicate(kind, base, deps, fn, trackingId);
}

/** Evaluates the underlying predicate. Missing dependencies evaluate to false. */
export function evaluatePredicate(world: World, entity: Entity, predicate: Predicate): boolean {
    const { deps, fn } = predicate[$predicate];
    const data = new Array(deps.length);
    for (let i = 0; i < deps.length; i++) {
        if (!hasTrait(world, entity, deps[i])) return false;
        data[i] = getTrait(world, entity, deps[i]);
    }
    return !!fn(data as TraitDataTuple<Trait[]>);
}

export type PredicateTerm =
    | { type: 'predicate'; predicate: Predicate; previous: Set<number> }
    | { type: 'or'; traits: Trait[]; predicates: Predicate[] };

function evaluateTerm(world: World, entity: Entity, term: PredicateTerm): boolean {
    if (term.type === 'or') {
        for (const trait of term.traits) if (hasTrait(world, entity, trait)) return true;
        for (const predicate of term.predicates) {
            if (evaluatePredicate(world, entity, predicate)) return true;
        }
        return false;
    }

    const { predicate, previous } = term;
    const kind = predicate[$predicate].kind;
    const value = evaluatePredicate(world, entity, predicate);

    switch (kind) {
        case 'is':
            return value;
        case 'not':
            return !value;
        default: {
            const eid = getEntityId(entity);
            const was = previous.has(eid);
            if (value) previous.add(eid);
            else previous.delete(eid);
            if (kind === 'added') return value && !was;
            if (kind === 'removed') return !value && was;
            return value !== was;
        }
    }
}

export function filterByPredicates(
    world: World,
    entities: Entity[],
    terms: PredicateTerm[]
): Entity[] {
    const result: Entity[] = [];
    for (const entity of entities) {
        let match = true;
        // Evaluate every term so tracking predicates keep their state in sync.
        for (const term of terms) {
            if (!evaluateTerm(world, entity, term)) match = false;
        }
        if (match) result.push(entity);
    }
    return result;
}

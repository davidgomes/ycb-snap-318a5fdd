import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import { addTrait, getStore, hasTrait, removeTrait, trait } from '../trait/trait';
import { getTraitInstance, hasTraitInstance } from '../trait/trait-instance';
import type { TagTrait, Trait, TraitRecord } from '../trait/types';
import type { World } from '../world';

export const $predicate = Symbol.for('koota.predicate');

export type PredicateValues<T extends Trait[]> = {
    [K in keyof T]: T[K] extends Trait ? TraitRecord<T[K]> : never;
};

export type Predicate<T extends Trait[] = Trait[]> = TagTrait & {
    readonly [$predicate]: T;
};

export function createPredicate<T extends Trait[]>(
    dependencies: [...T],
    fn: (values: PredicateValues<T>) => unknown
): Predicate<T> {
    if (!Array.isArray(dependencies) || dependencies.length === 0) {
        throw new Error('Koota: A predicate requires at least one dependency trait.');
    }

    for (const dependency of dependencies as unknown[]) {
        if (
            isRelation(dependency) ||
            isRelationPair(dependency) ||
            (dependency as Trait)[$internal].relation
        ) {
            throw new Error('Koota: Relations cannot be used as predicate dependencies.');
        }
        if ((dependency as Trait)[$internal].type === 'tag') {
            throw new Error('Koota: Tags cannot be used as predicate dependencies.');
        }
    }

    const predicate = trait() as unknown as Predicate<T>;
    const deps = [...dependencies] as Trait[];

    Object.defineProperty(predicate, $predicate, { value: deps, enumerable: false });
    predicate[$internal].predicate = { dependencies: deps, fn: fn as (values: any[]) => unknown };

    for (const dependency of deps) {
        const predicates = dependency[$internal].predicates;
        if (!predicates.includes(predicate)) predicates.push(predicate);
    }

    return predicate;
}

function evaluatePredicate(world: World, entity: Entity, predicate: Trait): boolean {
    const { dependencies, fn } = predicate[$internal].predicate!;
    const eid = getEntityId(entity);
    const values = new Array(dependencies.length);

    for (let i = 0; i < dependencies.length; i++) {
        const dependency = dependencies[i];
        if (!hasTrait(world, entity, dependency)) return false;
        values[i] = dependency[$internal].get(eid, getStore(world, dependency));
    }

    return !!fn(values);
}

function updatePredicate(world: World, entity: Entity, predicate: Trait) {
    const result = evaluatePredicate(world, entity, predicate);
    if (result === hasTrait(world, entity, predicate)) return;

    if (result) addTrait(world, entity, predicate);
    else removeTrait(world, entity, predicate);

    // Changed(predicate) matches any truthiness transition.
    const ctx = world[$internal];
    const { generationId, bitflag } = getTraitInstance(ctx.traitInstances, predicate)!;
    const eid = getEntityId(entity);
    for (const changedMask of ctx.changedMasks.values()) {
        if (!changedMask[generationId]) changedMask[generationId] = [];
        changedMask[generationId][eid] = changedMask[generationId][eid] | 0 | bitflag;
    }
}

function schedulePredicate(world: World, entity: Entity, predicate: Trait) {
    const ctx = world[$internal];

    if (ctx.predicateDeferDepth > 0) {
        let pending = ctx.pendingPredicates.get(entity);
        if (!pending) {
            pending = new Set();
            ctx.pendingPredicates.set(entity, pending);
        }
        pending.add(predicate);
        return;
    }

    updatePredicate(world, entity, predicate);
}

/** Re-evaluate predicates that depend on a trait after it was added, set or removed. */
export function onPredicateDependencyChange(world: World, entity: Entity, dependency: Trait) {
    const predicates = dependency[$internal].predicates;
    if (predicates.length === 0) return;

    const traitInstances = world[$internal].traitInstances;
    for (let i = 0; i < predicates.length; i++) {
        const predicate = predicates[i];
        // Predicates are only evaluated in worlds where they have been registered.
        if (!hasTraitInstance(traitInstances, predicate)) continue;
        schedulePredicate(world, entity, predicate);
    }
}

/** Evaluate a newly registered predicate against every entity in the world. */
export function initPredicate(world: World, predicate: Trait) {
    const entities = world[$internal].entityIndex.dense.slice();
    for (let i = 0; i < entities.length; i++) {
        schedulePredicate(world, entities[i] as Entity, predicate);
    }
}

export function beginPredicateDefer(world: World) {
    world[$internal].predicateDeferDepth++;
}

export function endPredicateDefer(world: World) {
    const ctx = world[$internal];
    if (--ctx.predicateDeferDepth > 0) return;

    const pending = ctx.pendingPredicates;
    for (const [entity, predicates] of pending) {
        pending.delete(entity);
        if (!world.has(entity)) continue;
        for (const predicate of predicates) updatePredicate(world, entity, predicate);
    }
}

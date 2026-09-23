import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import {
    addTraitToEntity,
    getStore,
    hasTrait,
    registerTrait,
    removeTraitFromEntity,
    trait,
} from '../trait/trait';
import { getTraitInstance, hasTraitInstance } from '../trait/trait-instance';
import type { Trait } from '../trait/types';
import type { World } from '../world';
import type { Predicate, PredicateValues } from './types';

/**
 * Creates a predicate that matches entities by the values of their traits.
 * The predicate is re-evaluated whenever one of its dependencies is added or set.
 */
export function createPredicate<T extends Trait[]>(
    traits: [...T],
    fn: (values: PredicateValues<T>) => boolean
): Predicate<T> {
    for (const dependency of traits) {
        if (isRelation(dependency) || isRelationPair(dependency) || dependency[$internal].relation) {
            throw new Error('Koota: Relations cannot be used as predicate dependencies.');
        }
        if (dependency[$internal].type === 'tag') {
            throw new Error('Koota: Tags cannot be used as predicate dependencies.');
        }
    }

    // A predicate is a tag that is kept on the entity while the predicate is truthy,
    // which lets it reuse the bitmask machinery of queries and modifiers.
    const predicate = trait() as unknown as Predicate<T>;
    predicate[$internal].predicate = { traits: [...traits] as T, fn };

    return predicate;
}

export /* @inline @pure */ function isPredicate(trait: Trait): boolean {
    return trait[$internal].predicate !== undefined;
}

/** Links a newly registered predicate to its dependencies and evaluates it for every entity. */
export function setupPredicate(world: World, predicate: Trait) {
    const ctx = world[$internal];
    const { traits } = predicate[$internal].predicate!;

    for (const dependency of traits) {
        if (!hasTraitInstance(ctx.traitInstances, dependency)) registerTrait(world, dependency);
        getTraitInstance(ctx.traitInstances, dependency)!.predicates.push(predicate);
    }

    const { dense, aliveCount } = ctx.entityIndex;
    for (let i = 0; i < aliveCount; i++) {
        evaluatePredicate(world, dense[i], predicate);
    }
}

export function evaluatePredicate(world: World, entity: Entity, predicate: Trait) {
    const { traits, fn } = predicate[$internal].predicate!;
    const eid = getEntityId(entity);
    const values: unknown[] = [];

    let result = true;
    for (let i = 0; i < traits.length; i++) {
        const dependency = traits[i];
        if (!hasTrait(world, entity, dependency)) {
            result = false;
            break;
        }
        values[i] = dependency[$internal].get(eid, getStore(world, dependency));
    }

    if (result) result = !!fn(values);
    if (result === hasTrait(world, entity, predicate)) return;

    if (result) addTraitToEntity(world, entity, predicate);
    else removeTraitFromEntity(world, entity, predicate);
}

/** Re-evaluates every predicate depending on the trait, or queues it while deferred. */
export function updatePredicates(world: World, entity: Entity, trait: Trait) {
    const ctx = world[$internal];
    const instance = getTraitInstance(ctx.traitInstances, trait);
    if (!instance) return;

    const predicates = instance.predicates;
    if (predicates.length === 0) return;

    if (ctx.predicateDeferDepth > 0) {
        for (let i = 0; i < predicates.length; i++) {
            const predicate = predicates[i];
            let entities = ctx.deferredPredicates.get(predicate);
            if (!entities) {
                entities = new Set();
                ctx.deferredPredicates.set(predicate, entities);
            }
            entities.add(entity);
        }
        return;
    }

    for (let i = 0; i < predicates.length; i++) {
        evaluatePredicate(world, entity, predicates[i]);
    }
}

export /* @inline @pure */ function hasPredicates(world: World, trait: Trait): boolean {
    const instance = getTraitInstance(world[$internal].traitInstances, trait);
    return instance !== undefined && instance.predicates.length > 0;
}

export function deferPredicates(world: World) {
    world[$internal].predicateDeferDepth++;
}

export function flushPredicates(world: World) {
    const ctx = world[$internal];
    if (--ctx.predicateDeferDepth > 0) return;
    if (ctx.deferredPredicates.size === 0) return;

    const pending = ctx.deferredPredicates;
    ctx.deferredPredicates = new Map();

    for (const [predicate, entities] of pending) {
        for (const entity of entities) {
            if (world.has(entity)) evaluatePredicate(world, entity, predicate);
        }
    }
}

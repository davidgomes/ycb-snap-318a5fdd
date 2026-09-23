import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getAliveEntities, isEntityAlive } from '../entity/utils/entity-index';
import { getEntityId } from '../entity/utils/pack-entity';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import { addTrait, registerTrait, removeTrait, trait } from '../trait/trait';
import { getTraitInstance, hasTraitInstance } from '../trait/trait-instance';
import type { TagTrait, Trait } from '../trait/types';
import type { World } from '../world';
import { $predicate, $predicateTrait } from './symbols';
import type { Predicate, PredicateInstance, PredicateValues } from './types';

type PredicateTrait = TagTrait & { readonly [$predicateTrait]: Predicate };

/**
 * Creates a predicate that matches entities by the data of their traits.
 * The function receives the data of each dependency trait in order and is
 * re-evaluated whenever a dependency is added, set or removed.
 * Each call creates a distinct predicate, even for identical arguments.
 *
 * @example
 * ```ts
 * const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
 * const dying = world.query(IsLowHealth);
 * ```
 */
export function createPredicate<T extends Trait[]>(
    dependencies: [...T],
    fn: (values: PredicateValues<T>) => boolean
): Predicate<T> {
    if (dependencies.length === 0) {
        throw new Error('Koota: A predicate needs at least one dependency trait.');
    }

    for (const dependency of dependencies) {
        if (isRelation(dependency) || isRelationPair(dependency) || dependency?.[$internal]?.relation) {
            throw new Error('Koota: Relations cannot be predicate dependencies.');
        }
        if (typeof dependency !== 'function' || !dependency[$internal]) {
            throw new Error('Koota: Predicate dependencies must be traits.');
        }
        if (dependency[$internal].type === 'tag') {
            throw new Error('Koota: Tags cannot be predicate dependencies since they have no data.');
        }
    }

    const predicateTrait = trait();

    const predicate = Object.freeze({
        [$predicate]: true,
        [$internal]: {
            trait: predicateTrait,
            dependencies: dependencies.slice() as T,
            fn: fn as (values: unknown[]) => boolean,
        },
    }) as Predicate<T>;

    Object.defineProperty(predicateTrait, $predicateTrait, {
        value: predicate,
        writable: false,
        enumerable: false,
        configurable: false,
    });

    return predicate;
}

/**
 * Check if a trait is the tag backing a predicate.
 */
export /* @inline @pure */ function isPredicateTrait(trait: Trait): trait is PredicateTrait {
    return $predicateTrait in trait;
}

/**
 * Setup a predicate in a world. Called during registration of its trait
 * to hook it into its dependencies and match the existing entities.
 */
export function setupPredicate(world: World, predicateTrait: PredicateTrait): void {
    const ctx = world[$internal];
    const predicate = predicateTrait[$predicateTrait];

    const dependencies = predicate[$internal].dependencies.map((dependency) => {
        if (!hasTraitInstance(ctx.traitInstances, dependency)) registerTrait(world, dependency);
        return getTraitInstance(ctx.traitInstances, dependency)!;
    });

    const instance: PredicateInstance = {
        predicate,
        trait: getTraitInstance(ctx.traitInstances, predicateTrait)!,
        dependencies,
        pending: new Set(),
    };

    for (const dependency of dependencies) {
        if (!dependency.predicates.includes(instance)) dependency.predicates.push(instance);
    }

    const entities = getAliveEntities(ctx.entityIndex);
    // An in-progress updateEach can still overwrite the values evaluated here.
    const deferred = ctx.predicateDeferDepth > 0;

    for (let i = 0; i < entities.length; i++) {
        evaluatePredicate(world, instance, entities[i]);
        if (deferred) instance.pending.add(entities[i]);
    }

    if (deferred) ctx.pendingPredicates.add(instance);
}

/**
 * Evaluate a predicate for an entity, adding or removing the predicate's trait
 * so that queries update through the regular trait machinery.
 */
function evaluatePredicate(world: World, instance: PredicateInstance, entity: Entity): void {
    const ctx = world[$internal];
    if (!isEntityAlive(ctx.entityIndex, entity)) return;

    const eid = getEntityId(entity);
    const entityMasks = ctx.entityMasks;
    const dependencies = instance.dependencies;

    let matches = true;
    for (let i = 0; i < dependencies.length; i++) {
        const { generationId, bitflag } = dependencies[i];
        if ((entityMasks[generationId][eid] & bitflag) === 0) {
            matches = false;
            break;
        }
    }

    if (matches) {
        const values: unknown[] = [];
        for (let i = 0; i < dependencies.length; i++) {
            const dependency = dependencies[i];
            values.push(dependency.trait[$internal].get(eid, dependency.store));
        }
        matches = !!instance.predicate[$internal].fn(values);
    }

    const { trait, generationId, bitflag } = instance.trait;
    const hasTrait = (entityMasks[generationId][eid] & bitflag) !== 0;

    if (matches && !hasTrait) addTrait(world, entity, trait);
    else if (!matches && hasTrait) removeTrait(world, entity, trait);
}

/**
 * Re-evaluate the predicates that depend on a trait for an entity.
 * While predicates are deferred the entity is queued instead.
 */
export function reevaluatePredicates(world: World, entity: Entity, trait: Trait): void {
    const ctx = world[$internal];
    const predicates = getTraitInstance(ctx.traitInstances, trait)?.predicates;
    if (!predicates || predicates.length === 0) return;

    for (let i = 0; i < predicates.length; i++) {
        const instance = predicates[i];

        if (ctx.predicateDeferDepth > 0) {
            instance.pending.add(entity);
            ctx.pendingPredicates.add(instance);
        } else {
            evaluatePredicate(world, instance, entity);
        }
    }
}

/**
 * Defer predicate re-evaluation until a matching call to resumePredicates.
 */
export function deferPredicates(world: World): void {
    world[$internal].predicateDeferDepth++;
}

/**
 * End a deferral started with deferPredicates. Once no deferrals remain,
 * every queued entity is re-evaluated.
 */
export function resumePredicates(world: World): void {
    const ctx = world[$internal];
    if (--ctx.predicateDeferDepth > 0 || ctx.pendingPredicates.size === 0) return;

    const instances = [...ctx.pendingPredicates];
    ctx.pendingPredicates.clear();

    for (const instance of instances) {
        const entities = [...instance.pending];
        instance.pending.clear();

        for (let i = 0; i < entities.length; i++) {
            evaluatePredicate(world, instance, entities[i]);
        }
    }
}

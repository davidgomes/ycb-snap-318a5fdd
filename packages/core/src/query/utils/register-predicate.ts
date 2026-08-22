import { $internal } from '../../common';
import type { World } from '../../world';
import { getTraitInstance, hasTraitInstance } from '../../trait/trait-instance';
import { registerTrait } from '../../trait/trait';
import type { TraitInstance } from '../../trait/types';
import type { QueryInstance } from '../types';
import type { PredicateModifier } from '../modifiers/predicate';

export function registerPredicateDependencies(
    world: World,
    query: QueryInstance,
    modifier: PredicateModifier
): void {
    const ctx = world[$internal];
    const deps = modifier.traits;

    for (let i = 0; i < deps.length; i++) {
        const trait = deps[i];
        if (!hasTraitInstance(ctx.traitInstances, trait)) registerTrait(world, trait);
        const instance = getTraitInstance(ctx.traitInstances, trait)!;
        instance.predicateQueries.add(query);

        if (!query.traits.includes(trait)) {
            query.traits.push(trait);
        }
    }
}

export function registerPredicateTraitInstances(
    world: World,
    query: QueryInstance,
    modifier: PredicateModifier
): void {
    const ctx = world[$internal];

    for (let i = 0; i < modifier.traits.length; i++) {
        const trait = modifier.traits[i];
        if (!hasTraitInstance(ctx.traitInstances, trait)) registerTrait(world, trait);
        const instance = getTraitInstance(ctx.traitInstances, trait)!;

        if (!query.traitInstances.all.includes(instance)) {
            query.traitInstances.all.push(instance);
        }
    }
}

export function ensurePredicateGenerations(query: QueryInstance): void {
    query.generations = query.traitInstances.all
        .map((c) => c.generationId)
        .reduce((a: number[], v) => {
            if (a.includes(v)) return a;
            a.push(v);
            return a;
        }, []);
}

export function rebuildStaticBitmasks(query: QueryInstance): void {
    query.staticBitmasks = query.generations.map((generationId) => {
        const required = query.traitInstances.required
            .filter((c) => c.generationId === generationId)
            .reduce((a, c) => a | c.bitflag, 0);

        const forbidden = query.traitInstances.forbidden
            .filter((c) => c.generationId === generationId)
            .reduce((a, c) => a | c.bitflag, 0);

        const or = query.traitInstances.or
            .filter((c) => c.generationId === generationId)
            .reduce((a, c) => a | c.bitflag, 0);

        return { required, forbidden, or };
    });
}

export function addPredicateToQueryTraits(query: QueryInstance, instance: TraitInstance): void {
    if (!query.traitInstances.all.includes(instance)) {
        query.traitInstances.all.push(instance);
    }
}

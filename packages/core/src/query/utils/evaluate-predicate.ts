import { getTrait, hasTrait } from '../../trait/trait';
import type { Entity } from '../../entity/types';
import type { World } from '../../world';
import type { PredicateModifier } from '../modifiers/predicate';

export function evaluatePredicate(world: World, entity: Entity, modifier: PredicateModifier): boolean {
    const deps = modifier.traits;

    const values: unknown[] = [];
    for (let i = 0; i < deps.length; i++) {
        const trait = deps[i];
        if (!hasTrait(world, entity, trait)) return false;
        values.push(getTrait(world, entity, trait));
    }

    return modifier.predicate(values as never);
}

export function evaluateNotPredicate(
    world: World,
    entity: Entity,
    modifier: PredicateModifier
): boolean {
    const deps = modifier.traits;

    for (let i = 0; i < deps.length; i++) {
        if (!hasTrait(world, entity, deps[i])) return true;
    }

    const values = deps.map((trait) => getTrait(world, entity, trait));
    return !modifier.predicate(values as never);
}

export function getPredicateMatch(
    world: World,
    entity: Entity,
    modifier: PredicateModifier,
    negated = false
): boolean {
    return negated
        ? evaluateNotPredicate(world, entity, modifier)
        : evaluatePredicate(world, entity, modifier);
}

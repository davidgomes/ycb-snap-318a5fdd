import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { setChanged } from '../modifiers/changed';
import { addTrait, getTrait, hasTrait, removeTrait } from '../../trait/trait';
import type { Trait, TraitRecord } from '../../trait/types';
import { universe } from '../../universe/universe';
import type { World } from '../../world';

export type PredicateDefinition = {
    trait: Trait;
    dependencies: Trait[];
    evaluate: (data: TraitRecord<Trait>[]) => boolean;
    scratch: TraitRecord<Trait>[];
};

const predicatesByDependencyId: PredicateDefinition[][] = [];

export function registerPredicateDefinition(definition: PredicateDefinition): void {
    const deps = definition.dependencies;
    for (let i = 0; i < deps.length; i++) {
        const id = deps[i].id;
        if (id >= predicatesByDependencyId.length) {
            predicatesByDependencyId.length = id + 1;
        }
        const list = predicatesByDependencyId[id] ?? (predicatesByDependencyId[id] = []);
        list.push(definition);
    }
}

export function isPredicateTrait(trait: Trait): boolean {
    return trait[$internal].isPredicate === true;
}

export function evaluatePredicate(world: World, entity: Entity, definition: PredicateDefinition): boolean {
    const deps = definition.dependencies;
    const scratch = definition.scratch;

    for (let i = 0; i < deps.length; i++) {
        const trait = deps[i];
        if (!hasTrait(world, entity, trait)) return false;
        scratch[i] = getTrait(world, entity, trait)!;
    }

    return !!definition.evaluate(scratch);
}

export function syncPredicateResult(
    world: World,
    entity: Entity,
    definition: PredicateDefinition,
    trackChange = true
): void {
    if (!world.has(entity)) return;

    const matches = evaluatePredicate(world, entity, definition);
    const had = hasTrait(world, entity, definition.trait);

    if (matches === had) return;

    if (matches) {
        addTrait(world, entity, definition.trait);
        if (trackChange) setChanged(world, entity, definition.trait);
        return;
    }

    if (trackChange) setChanged(world, entity, definition.trait);
    removeTrait(world, entity, definition.trait);
}

export function notifyPredicateDependencies(world: World, entity: Entity, trait: Trait): void {
    const definitions = predicatesByDependencyId[trait.id];
    if (!definitions || definitions.length === 0) return;

    const ctx = world[$internal];
    if (ctx.predicateEvalDepth > 0) {
        const pending = ctx.pendingPredicateEvals;
        for (let i = 0; i < definitions.length; i++) {
            pending.push(entity, definitions[i]);
        }
        return;
    }

    for (let i = 0; i < definitions.length; i++) {
        syncPredicateResult(world, entity, definitions[i]);
    }
}

export function beginDeferredPredicateEvaluation(world: World): void {
    world[$internal].predicateEvalDepth++;
}

export function endDeferredPredicateEvaluation(world: World): void {
    const ctx = world[$internal];
    ctx.predicateEvalDepth--;
    if (ctx.predicateEvalDepth > 0) return;

    const pending = ctx.pendingPredicateEvals;
    if (pending.length === 0) return;

    const seen = new Set<string>();
    for (let i = 0; i < pending.length; i += 2) {
        const entity = pending[i] as Entity;
        const definition = pending[i + 1] as PredicateDefinition;
        const key = `${entity}:${definition.trait.id}`;
        if (seen.has(key)) continue;
        seen.add(key);
        syncPredicateResult(world, entity, definition);
    }

    pending.length = 0;
}

export function evaluatePredicateOnWorld(world: World, definition: PredicateDefinition): void {
    const ctx = world[$internal];
    if (!world.isInitialized) return;

    const entities = ctx.entityIndex.dense;
    for (let i = 0; i < entities.length; i++) {
        const entity = entities[i];
        if (entity === ctx.worldEntity) continue;
        syncPredicateResult(world, entity, definition, false);
    }
}

export function evaluatePredicateOnAllWorlds(definition: PredicateDefinition): void {
    const worlds = universe.worlds;
    for (let i = 0; i < worlds.length; i++) {
        const world = worlds[i];
        if (!world) continue;
        evaluatePredicateOnWorld(world, definition);
    }
}

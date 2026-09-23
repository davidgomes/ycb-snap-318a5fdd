import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import { hasRelationToTarget } from '../../relation/relation';
import { hasTrait, registerTrait } from '../../trait/trait';
import { getTraitInstance, hasTraitInstance } from '../../trait/trait-instance';
import type { ExtractTrackingTraits, TrackingModifierInput, Trait } from '../../trait/types';
import { universe } from '../../universe/universe';
import type { World } from '../../world';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';
import { matchTrackingQuery } from '../utils/check-query-tracking-with-relations';
import { recordPairEvent } from '../utils/pair-tracking';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';
import { resolveTrackingInputs } from './resolve-tracking-inputs';

export function createChanged() {
    const id = createTrackingId('change');

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends TrackingModifierInput[]>(
        ...inputs: T
    ): Modifier<ExtractTrackingTraits<T>, `changed-${number}`> => {
        const resolved = resolveTrackingInputs(inputs);
        return createModifier(
            `changed-${id}`,
            id,
            resolved.traits as ExtractTrackingTraits<T>,
            resolved
        );
    };
}

/** @inline */
function markChanged(world: World, entity: Entity, trait: Trait) {
    const ctx = world[$internal];

    // Early exit if the trait is not on the entity.
    if (!hasTrait(world, entity, trait)) return;

    // Register the trait if it's not already registered.
    if (!hasTraitInstance(ctx.traitInstances, trait)) registerTrait(world, trait);
    const data = getTraitInstance(ctx.traitInstances, trait)!;

    // Mark the trait as changed in bitmasks for Changed modifiers.
    const eid = getEntityId(entity);
    const { generationId, bitflag } = data;

    for (const changedMask of ctx.changedMasks.values()) {
        if (!changedMask[generationId]) changedMask[generationId] = [];
        if (!changedMask[generationId][eid]) changedMask[generationId][eid] = 0;
        changedMask[generationId][eid] |= bitflag;
    }

    // Update tracking queries with change event
    for (const query of data.trackingQueries) {
        if (!query.hasChangedModifiers) continue;
        if (!query.changedTraits.has(trait)) continue;

        const match = matchTrackingQuery(world, query, entity, 'change', generationId, bitflag);
        if (match) query.add(entity);
        else query.remove(world, entity);
    }

    return data;
}

export function setChanged(world: World, entity: Entity, trait: Trait) {
    const data = markChanged(world, entity, trait);
    if (!data) return;
    for (const sub of data.changeSubscriptions) sub(entity);
}

export function setPairChanged(world: World, entity: Entity, trait: Trait, target: Entity) {
    const relation = trait[$internal].relation;
    if (!relation || !hasRelationToTarget(world, relation, entity, target)) return;

    recordPairEvent(world, entity, relation, target, 'change');

    const data = markChanged(world, entity, trait);
    if (!data) return;
    for (const sub of data.changeSubscriptions) sub(entity, target);
}

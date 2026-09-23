import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import { hasRelationToTarget } from '../../relation/relation';
import { hasTrait, registerTrait } from '../../trait/trait';
import { getTraitInstance, hasTraitInstance } from '../../trait/trait-instance';
import type { Trait } from '../../trait/types';
import { universe } from '../../universe/universe';
import type { World } from '../../world';
import { createModifier } from '../modifier';
import type { Modifier } from '../types';
import { checkQueryTrackingWithRelations } from '../utils/check-query-tracking-with-relations';
import { recordPairEvent } from '../utils/pair-tracking';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';
import { normalizeTrackingInputs, type TrackingInput, type TrackingTraits } from './tracking-inputs';

export function createChanged() {
    const id = createTrackingId();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends TrackingInput[]>(
        ...inputs: T
    ): Modifier<TrackingTraits<T>, `changed-${number}`> => {
        const { traits, pairTargets } = normalizeTrackingInputs(inputs);
        return createModifier(`changed-${id}`, id, traits, pairTargets) as Modifier<
            TrackingTraits<T>,
            `changed-${number}`
        >;
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

        const match =
            query.relationFilters && query.relationFilters.length > 0
                ? checkQueryTrackingWithRelations(
                      world,
                      query,
                      entity,
                      'change',
                      generationId,
                      bitflag
                  )
                : query.checkTracking(world, entity, 'change', generationId, bitflag);
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
    if (!hasTrait(world, entity, trait)) return;

    const relation = trait[$internal].relation;
    if (relation && !hasRelationToTarget(world, relation, entity, target)) return;
    if (relation) recordPairEvent(world, entity, relation, target, 'change');

    const data = markChanged(world, entity, trait);
    if (!data) return;
    for (const sub of data.changeSubscriptions) sub(entity, target);
}

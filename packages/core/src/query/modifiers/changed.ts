import { $internal } from '../../common';
import type { Entity } from '../../entity/types';
import { getEntityId } from '../../entity/utils/pack-entity';
import { getRelationTargets, hasRelationToTarget } from '../../relation/relation';
import type { RelationPair } from '../../relation/types';
import { hasTrait, registerTrait } from '../../trait/trait';
import { getTraitInstance, hasTraitInstance } from '../../trait/trait-instance';
import type { ExtractTraits, Trait, TrackingModifierInput } from '../../trait/types';
import { universe } from '../../universe/universe';
import type { World } from '../../world';
import { createTrackingModifier } from '../modifier';
import type { Modifier } from '../types';
import { checkQueryTrackingWithRelations } from '../utils/check-query-tracking-with-relations';
import { enablePairChangeTicks, trackPairEvent } from '../utils/pair-tracking';
import { createTrackingId, setTrackingMasks } from '../utils/tracking-cursor';

export function createChanged() {
    const id = createTrackingId();
    enablePairChangeTicks();

    for (const world of universe.worlds) {
        if (!world) continue;
        setTrackingMasks(world, id);
    }

    return <T extends TrackingModifierInput[]>(
        ...inputs: T
    ): Modifier<ExtractTraits<T>, `changed-${number}`> =>
        createTrackingModifier(`changed-${id}` as `changed-${number}`, id, inputs);
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
    const data = markChanged(world, entity, trait);
    if (!data) return;

    trackPairEvent(world, trait[$internal].relation!, entity, target, 'change');

    for (const sub of data.changeSubscriptions) sub(entity, target);
}

/** Flag a relation pair as changed. A `'*'` target flags every target the entity has. */
export function setRelationPairChanged(world: World, entity: Entity, pair: RelationPair) {
    const { relation, target } = pair[$internal];
    const trait = relation[$internal].trait;

    if (target === '*') {
        for (const t of getRelationTargets(world, relation, entity)) {
            setPairChanged(world, entity, trait, t);
        }
    } else if (hasRelationToTarget(world, relation, entity, target)) {
        setPairChanged(world, entity, trait, target);
    }
}

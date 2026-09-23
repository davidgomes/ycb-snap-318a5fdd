// Add methods to the Number prototype so it can be used as an entity.
// This lets us keep the performance of raw numbers over using objects
// and the convenience of using methods. Type guards are used to ensure
// that the methods are only called on entities.

import { $internal } from '../common';
import {
    deferredGet,
    deferredHas,
    deferredTargets,
    flushIfPending,
    shouldReadDeferred,
} from '../deferred/deferred';
import { setChanged } from '../query/modifiers/changed';
import { getFirstRelationTarget, getRelationTargets, hasRelationPair } from '../relation/relation';
import type { Relation, RelationPair } from '../relation/types';
import { isRelationPair } from '../relation/utils/is-relation';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import type { ConfigurableTrait, Trait } from '../trait/types';
import { destroyEntity, getEntityWorld } from './entity';
import type { Entity } from './types';
import { isEntityAlive } from './utils/entity-index';
import { getEntityGeneration, getEntityId } from './utils/pack-entity';

function stillAlive(world: ReturnType<typeof getEntityWorld>, entity: Entity, wasAlive: boolean) {
    return isEntityAlive(world[$internal].entityIndex, entity) || !wasAlive;
}

// @ts-expect-error
Number.prototype.add = function (this: Entity, ...traits: ConfigurableTrait[]) {
    const world = getEntityWorld(this);
    const wasAlive = isEntityAlive(world[$internal].entityIndex, this);
    flushIfPending(world, this);
    if (!stillAlive(world, this, wasAlive)) return;
    return addTrait(world, this, ...traits);
};

// @ts-expect-error
Number.prototype.remove = function (this: Entity, ...traits: (Trait | RelationPair)[]) {
    const world = getEntityWorld(this);
    const wasAlive = isEntityAlive(world[$internal].entityIndex, this);
    flushIfPending(world, this);
    if (!stillAlive(world, this, wasAlive)) return;
    return removeTrait(world, this, ...traits);
};

// @ts-expect-error
Number.prototype.has = function (this: Entity, trait: Trait | RelationPair) {
    const world = getEntityWorld(this);
    if (shouldReadDeferred(world)) return deferredHas(world, this, trait);
    if (isRelationPair(trait)) return hasRelationPair(world, this, trait);
    return /* @inline @pure */ hasTrait(world, this, trait);
};

// @ts-expect-error
Number.prototype.destroy = function (this: Entity) {
    const world = getEntityWorld(this);
    const wasAlive = isEntityAlive(world[$internal].entityIndex, this);
    flushIfPending(world, this);
    if (!stillAlive(world, this, wasAlive)) return;
    return destroyEntity(world, this);
};

// @ts-expect-error
Number.prototype.changed = function (this: Entity, trait: Trait) {
    return setChanged(getEntityWorld(this), this, trait);
};

// @ts-expect-error
Number.prototype.get = function (this: Entity, trait: Trait | RelationPair) {
    const world = getEntityWorld(this);
    if (shouldReadDeferred(world)) return deferredGet(world, this, trait);
    return getTrait(world, this, trait);
};

// @ts-expect-error
Number.prototype.set = function (
    this: Entity,
    trait: Trait | RelationPair,
    value: any,
    triggerChanged = true
) {
    const world = getEntityWorld(this);
    const wasAlive = isEntityAlive(world[$internal].entityIndex, this);
    flushIfPending(world, this);
    if (!stillAlive(world, this, wasAlive)) return;
    setTrait(world, this, trait, value, triggerChanged);
};

//@ts-expect-error
Number.prototype.targetsFor = function (this: Entity, relation: Relation<any>) {
    const world = getEntityWorld(this);
    if (shouldReadDeferred(world)) return deferredTargets(world, this, relation);
    return getRelationTargets(world, relation, this);
};

//@ts-expect-error
Number.prototype.targetFor = function (this: Entity, relation: Relation<any>) {
    const world = getEntityWorld(this);
    if (shouldReadDeferred(world)) return deferredTargets(world, this, relation)[0];
    return getFirstRelationTarget(world, relation, this);
};

//@ts-expect-error
Number.prototype.id = function (this: Entity) {
    return getEntityId(this);
};

// @ts-expect-error
Number.prototype.generation = function (this: Entity) {
    return getEntityGeneration(this);
};

//@ts-expect-error
Number.prototype.isAlive = function (this: Entity) {
    const world = getEntityWorld(this);
    const entityIndex = world[$internal].entityIndex;
    return isEntityAlive(entityIndex, this);
};

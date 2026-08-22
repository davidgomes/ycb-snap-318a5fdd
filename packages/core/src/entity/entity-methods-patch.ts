// Add methods to the Number prototype so it can be used as an entity.
// This lets us keep the performance of raw numbers over using objects
// and the convenience of using methods. Type guards are used to ensure
// that the methods are only called on entities.

import { $internal } from '../common';
import {
    addAspect,
    getAspect,
    hasAspect,
    isAspect,
    removeAspect,
    setAspect,
} from '../aspect/aspect';
import type { ConfigurableAspect } from '../aspect/types';
import type { Aspect } from '../aspect/types';
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

// @ts-expect-error
Number.prototype.add = function (this: Entity, ...traits: (ConfigurableTrait | ConfigurableAspect)[]) {
    const world = getEntityWorld(this);
    for (let i = 0; i < traits.length; i++) {
        const item = traits[i];
        if (Array.isArray(item) && isAspect(item[0])) {
            addAspect(world, this, item[0], item[1]);
        } else if (isAspect(item)) {
            addAspect(world, this, item);
        } else {
            addTrait(world, this, item);
        }
    }
};

// @ts-expect-error
Number.prototype.remove = function (this: Entity, ...traits: (Trait | RelationPair | Aspect)[]) {
    const world = getEntityWorld(this);
    for (let i = 0; i < traits.length; i++) {
        const item = traits[i];
        if (isAspect(item)) removeAspect(world, this, item);
        else removeTrait(world, this, item);
    }
};

// @ts-expect-error
Number.prototype.has = function (this: Entity, trait: Trait | RelationPair | Aspect) {
    const world = getEntityWorld(this);
    if (isAspect(trait)) return hasAspect(world, this, trait);
    if (isRelationPair(trait)) return hasRelationPair(world, this, trait);
    return /* @inline @pure */ hasTrait(world, this, trait);
};

// @ts-expect-error
Number.prototype.destroy = function (this: Entity) {
    return destroyEntity(getEntityWorld(this), this);
};

// @ts-expect-error
Number.prototype.changed = function (this: Entity, trait: Trait) {
    return setChanged(getEntityWorld(this), this, trait);
};

// @ts-expect-error
Number.prototype.get = function (this: Entity, trait: Trait | RelationPair | Aspect) {
    const world = getEntityWorld(this);
    if (isAspect(trait)) return getAspect(world, this, trait);
    return getTrait(getEntityWorld(this), this, trait);
};

// @ts-expect-error
Number.prototype.set = function (
    this: Entity,
    trait: Trait | RelationPair | Aspect,
    value: any,
    triggerChanged = true
) {
    const world = getEntityWorld(this);
    if (isAspect(trait)) return setAspect(world, this, trait, value, triggerChanged);
    setTrait(world, this, trait, value, triggerChanged);
};

//@ts-expect-error
Number.prototype.targetsFor = function (this: Entity, relation: Relation<any>) {
    return getRelationTargets(getEntityWorld(this), relation, this);
};

//@ts-expect-error
Number.prototype.targetFor = function (this: Entity, relation: Relation<any>) {
    return getFirstRelationTarget(getEntityWorld(this), relation, this);
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

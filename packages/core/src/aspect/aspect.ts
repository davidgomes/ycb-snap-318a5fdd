import { $internal } from '../common';
import type { Entity } from '../entity/types';
import {
    addTrait,
    getTrait,
    hasTrait,
    removeTrait,
    setTrait,
} from '../trait/trait';
import type { Trait } from '../trait/types';
import type { World } from '../world';
import type { Aspect, AspectInput, AspectRecord, AspectTuple, ConfigurableAspect } from './types';
import { buildAspectSchema } from './utils/build-aspect-schema';
import { flattenAspectConstituents } from './utils/flatten-aspect-constituents';
import { isAspect } from './utils/is-aspect';
import { $aspect } from './symbols';

let aspectId = 0;

export function createAspect<T extends AspectInput[]>(
    ...constituents: T
): Aspect {
    if (constituents.length < 2) {
        throw new Error('Koota: createAspect requires at least two constituents.');
    }

    const traits = flattenAspectConstituents(constituents);
    if (traits.length < 2) {
        throw new Error('Koota: createAspect requires at least two constituents.');
    }

    const { fieldMap, schema, dataTraits } = buildAspectSchema(traits);
    const id = aspectId++;

    const aspect = {
        [$aspect]: true,
        id,
        traits: Object.freeze(traits.slice()),
        schema,
        [$internal]: {
            fieldMap,
            dataTraits,
        },
    } as Aspect;

    Object.defineProperty(aspect, 'id', {
        value: id,
        writable: false,
        enumerable: true,
        configurable: false,
    });

    Object.defineProperty(aspect, 'traits', {
        value: aspect.traits,
        writable: false,
        enumerable: true,
        configurable: false,
    });

    Object.defineProperty(aspect, 'schema', {
        value: aspect.schema,
        writable: false,
        enumerable: true,
        configurable: false,
    });

    return aspect;
}

export function hasAspect(world: World, entity: Entity, aspect: Aspect): boolean {
    const traits = aspect.traits;
    for (let i = 0; i < traits.length; i++) {
        if (!hasTrait(world, entity, traits[i])) return false;
    }
    return true;
}

export function getAspect<A extends Aspect>(
    world: World,
    entity: Entity,
    aspect: A
): AspectRecord<A> | undefined {
    if (!hasAspect(world, entity, aspect)) return undefined;

    const { fieldMap } = aspect[$internal];
    const result: Record<string, unknown> = {};

    for (const [key, trait] of fieldMap) {
        const data = getTrait(world, entity, trait);
        if (data === undefined) return undefined;

        if (trait[$internal].type === 'aos') {
            Object.assign(result, data as Record<string, unknown>);
        } else {
            result[key] = (data as Record<string, unknown>)[key];
        }
    }

    return result as AspectRecord<A>;
}

function distributeAspectValue(
    aspect: Aspect,
    value: Record<string, unknown>
): Map<Trait, Record<string, unknown>> {
    const { fieldMap } = aspect[$internal];
    const byTrait = new Map<Trait, Record<string, unknown>>();

    for (const key in value) {
        const trait = fieldMap.get(key);
        if (!trait) continue;

        let fields = byTrait.get(trait);
        if (!fields) {
            fields = {};
            byTrait.set(trait, fields);
        }

        fields[key] = value[key];
    }

    return byTrait;
}

export function setAspect<A extends Aspect>(
    world: World,
    entity: Entity,
    aspect: A,
    value: AspectRecord<A> | ((prev: AspectRecord<A> | undefined) => AspectRecord<A>),
    triggerChanged = true
) {
    let resolved = value;
    if (resolved instanceof Function) {
        resolved = resolved(getAspect(world, entity, aspect));
    }

    const byTrait = distributeAspectValue(aspect, resolved as Record<string, unknown>);

    for (const [trait, fields] of byTrait) {
        if (trait[$internal].type === 'aos') {
            setTrait(world, entity, trait, fields, triggerChanged);
        } else {
            const existing = getTrait(world, entity, trait) ?? {};
            setTrait(world, entity, trait, { ...existing, ...fields }, triggerChanged);
        }
    }
}

export function addAspect(
    world: World,
    entity: Entity,
    aspect: Aspect,
    value?: Record<string, unknown>
) {
    const traits = aspect.traits;
    const byTrait = value ? distributeAspectValue(aspect, value) : null;

    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        if (hasTrait(world, entity, trait)) {
            if (byTrait?.has(trait)) {
                const fields = byTrait.get(trait)!;
                if (trait[$internal].type === 'aos') {
                    setTrait(world, entity, trait, fields, false);
                } else {
                    const existing = getTrait(world, entity, trait) ?? {};
                    setTrait(world, entity, trait, { ...existing, ...fields }, false);
                }
            }
            continue;
        }

        const params = byTrait?.get(trait);
        if (params) addTrait(world, entity, [trait, params]);
        else addTrait(world, entity, trait);
    }
}

export function removeAspect(world: World, entity: Entity, aspect: Aspect) {
    removeTrait(world, entity, ...aspect.traits);
}

export function isEntityAspectComplete(
    world: World,
    entity: Entity,
    aspect: Aspect,
    excludeTrait?: Trait
): boolean {
    const traits = aspect.traits;
    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        if (excludeTrait && trait === excludeTrait) continue;
        if (!hasTrait(world, entity, trait)) return false;
    }
    return true;
}

export function wasEntityAspectCompleteBeforeAdd(
    world: World,
    entity: Entity,
    aspect: Aspect,
    addedTrait: Trait
): boolean {
    return isEntityAspectComplete(world, entity, aspect, addedTrait);
}

export function fireAspectChange(world: World, entity: Entity, aspect: Aspect) {
    if (!hasAspect(world, entity, aspect)) return;

    const ctx = world[$internal];
    const subscriptions = ctx.aspectChangeSubscriptions.get(aspect);
    if (!subscriptions) return;

    for (const sub of subscriptions) sub(entity);

    const dataTraits = aspect[$internal].dataTraits;
    for (let i = 0; i < dataTraits.length; i++) {
        ctx.trackedTraits.add(dataTraits[i]);
    }
}

export function notifyAspectConstituentChanged(
    world: World,
    entity: Entity,
    trait: Trait
) {
    const ctx = world[$internal];
    const aspects = ctx.traitAspects.get(trait);
    if (!aspects) return;

    for (const aspect of aspects) {
        if (!hasAspect(world, entity, aspect)) continue;

        const subscriptions = ctx.aspectChangeSubscriptions.get(aspect);
        if (subscriptions) {
            for (const sub of subscriptions) sub(entity);
        }
    }
}

export function notifyAspectConstituentAdded(
    world: World,
    entity: Entity,
    trait: Trait
) {
    const ctx = world[$internal];
    const aspects = ctx.traitAspects.get(trait);
    if (!aspects) return;

    for (const aspect of aspects) {
        if (!wasEntityAspectCompleteBeforeAdd(world, entity, aspect, trait)) continue;
        if (!hasAspect(world, entity, aspect)) continue;

        const subscriptions = ctx.aspectAddSubscriptions.get(aspect);
        if (subscriptions) {
            for (const sub of subscriptions) sub(entity);
        }
    }
}

export function notifyAspectConstituentRemoved(
    world: World,
    entity: Entity,
    aspect: Aspect
) {
    const subscriptions = world[$internal].aspectRemoveSubscriptions.get(aspect);
    if (!subscriptions) return;

    for (const sub of subscriptions) sub(entity);
}

export function registerAspectSubscriptions(world: World, aspect: Aspect) {
    const ctx = world[$internal];
    const traits = aspect.traits;

    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        let aspects = ctx.traitAspects.get(trait);
        if (!aspects) {
            aspects = new Set();
            ctx.traitAspects.set(trait, aspects);
        }
        aspects.add(aspect);
    }
}

export function subscribeAspectAdd(
    world: World,
    aspect: Aspect,
    callback: (entity: Entity) => void
) {
    const ctx = world[$internal];
    registerAspectSubscriptions(world, aspect);

    let subscriptions = ctx.aspectAddSubscriptions.get(aspect);
    if (!subscriptions) {
        subscriptions = new Set();
        ctx.aspectAddSubscriptions.set(aspect, subscriptions);
    }

    subscriptions.add(callback);

    return () => subscriptions!.delete(callback);
}

export function subscribeAspectRemove(
    world: World,
    aspect: Aspect,
    callback: (entity: Entity) => void
) {
    const ctx = world[$internal];
    registerAspectSubscriptions(world, aspect);

    let subscriptions = ctx.aspectRemoveSubscriptions.get(aspect);
    if (!subscriptions) {
        subscriptions = new Set();
        ctx.aspectRemoveSubscriptions.set(aspect, subscriptions);
    }

    subscriptions.add(callback);

    return () => subscriptions!.delete(callback);
}

export function subscribeAspectChange(
    world: World,
    aspect: Aspect,
    callback: (entity: Entity) => void
) {
    const ctx = world[$internal];
    registerAspectSubscriptions(world, aspect);

    let subscriptions = ctx.aspectChangeSubscriptions.get(aspect);
    if (!subscriptions) {
        subscriptions = new Set();
        ctx.aspectChangeSubscriptions.set(aspect, subscriptions);
    }

    subscriptions.add(callback);

    const dataTraits = aspect[$internal].dataTraits;
    for (let i = 0; i < dataTraits.length; i++) {
        ctx.trackedTraits.add(dataTraits[i]);
    }

    return () => {
        subscriptions!.delete(callback);
        if (subscriptions!.size === 0) {
            ctx.aspectChangeSubscriptions.delete(aspect);
        }
    };
}

export { isAspect };

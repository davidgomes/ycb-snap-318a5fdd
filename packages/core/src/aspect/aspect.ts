import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { setChanged } from '../query/modifiers/changed';
import { isOrderedTrait } from '../relation/ordered';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import { addTrait, getStore, hasTrait, registerTrait } from '../trait/trait';
import { getTraitInstance, hasTraitInstance } from '../trait/trait-instance';
import type { Trait, TraitInstance } from '../trait/types';
import type { World } from '../world';
import { $aspect } from './symbols';
import type { Aspect, AspectInput, FlattenAspectInputs } from './types';
import { isAspect } from './utils/is-aspect';

let aspectId = 0;

type FlattenedTraits<T extends AspectInput[]> =
    FlattenAspectInputs<T> extends infer F extends Trait[] ? F : Trait[];

function createAspect<T extends AspectInput[]>(...inputs: T): Aspect<FlattenedTraits<T>> {
    const traits: Trait[] = [];

    for (const input of inputs) {
        if (isAspect(input)) {
            for (const trait of input.traits) {
                if (!traits.includes(trait)) traits.push(trait);
            }
            continue;
        }

        validateConstituent(input);
        if (!traits.includes(input)) traits.push(input);
    }

    if (traits.length < 2) {
        throw new Error('Koota: An aspect requires at least two traits.');
    }

    const fields: string[][] = [];
    const dataTraits: Trait[] = [];
    const dataFields: string[][] = [];
    const schema: Record<string, unknown> = {};

    for (const trait of traits) {
        const type = trait[$internal].type;
        let traitFields: string[] = [];

        if (type === 'soa') {
            traitFields = Object.keys(trait.schema);
            for (const key of traitFields) {
                if (Object.hasOwn(schema, key)) throwOverlappingField(key);
                schema[key] = trait.schema[key];
            }
        } else if (type === 'aos') {
            // AoS fields can only be known by inspecting an instance.
            const sample = (trait.schema as () => unknown)();
            if (sample === null || typeof sample !== 'object' || Array.isArray(sample)) {
                throw new Error(
                    'Koota: Callback-based traits must return an object to be used in an aspect.'
                );
            }
            traitFields = Object.keys(sample);
            for (const key of traitFields) {
                if (Object.hasOwn(schema, key)) throwOverlappingField(key);
                schema[key] = (sample as Record<string, unknown>)[key];
            }
        }

        fields.push(traitFields);
        if (type !== 'tag') {
            dataTraits.push(trait);
            dataFields.push(traitFields);
        }
    }

    const id = aspectId++;
    const aspect = Object.assign((params?: Record<string, unknown>) => [aspect, params], {
        [$internal]: { fields, dataTraits, dataFields },
    }) as unknown as Aspect<FlattenedTraits<T>>;

    Object.defineProperty(aspect, $aspect, {
        value: true,
        writable: false,
        enumerable: false,
        configurable: false,
    });

    Object.defineProperty(aspect, 'id', {
        value: id,
        writable: false,
        enumerable: true,
        configurable: false,
    });

    Object.defineProperty(aspect, 'traits', {
        value: Object.freeze(traits),
        writable: false,
        enumerable: true,
        configurable: false,
    });

    Object.defineProperty(aspect, 'schema', {
        value: Object.freeze(schema),
        writable: false,
        enumerable: true,
        configurable: false,
    });

    return aspect;
}

export { createAspect };

function validateConstituent(input: unknown): asserts input is Trait {
    if (isRelation(input) || isRelationPair(input)) {
        throw new Error('Koota: Relations cannot be used in an aspect.');
    }

    const traitCtx = (input as Trait | undefined)?.[$internal];
    if (typeof input !== 'function' || !traitCtx || typeof traitCtx.get !== 'function') {
        throw new Error('Koota: An aspect can only be created from traits and aspects.');
    }

    if (traitCtx.relation || isOrderedTrait(input as Trait)) {
        throw new Error('Koota: Relations cannot be used in an aspect.');
    }
}

function throwOverlappingField(key: string): never {
    throw new Error(`Koota: The field "${key}" is defined by more than one trait in the aspect.`);
}

export function hasAspect(world: World, entity: Entity, aspect: Aspect): boolean {
    const traits = aspect.traits;
    for (let i = 0; i < traits.length; i++) {
        if (!hasTrait(world, entity, traits[i])) return false;
    }
    return true;
}

/**
 * Copy the fields of the aspect's data traits into `out`.
 * With `onlyPresent`, traits the entity does not have are skipped instead of read.
 */
function readAspectFields(
    world: World,
    entity: Entity,
    aspect: Aspect,
    out: Record<string, unknown>,
    onlyPresent: boolean
) {
    const { dataTraits, dataFields } = aspect[$internal];
    const eid = getEntityId(entity);

    for (let i = 0; i < dataTraits.length; i++) {
        const trait = dataTraits[i];
        if (onlyPresent && !hasTrait(world, entity, trait)) continue;

        const store = getStore(world, trait) as any;
        const keys = dataFields[i];

        if (trait[$internal].type === 'soa') {
            for (let j = 0; j < keys.length; j++) out[keys[j]] = store[keys[j]][eid];
        } else {
            const record = store[eid];
            for (let j = 0; j < keys.length; j++) out[keys[j]] = record[keys[j]];
        }
    }

    return out;
}

export function getAspect(world: World, entity: Entity, aspect: Aspect) {
    if (!hasAspect(world, entity, aspect)) return undefined;
    return readAspectFields(world, entity, aspect, {}, false);
}

export function setAspect(
    world: World,
    entity: Entity,
    aspect: Aspect,
    value: any,
    triggerChanged = true
) {
    const { dataTraits, dataFields } = aspect[$internal];
    const eid = getEntityId(entity);

    if (value instanceof Function) value = value(readAspectFields(world, entity, aspect, {}, true));

    const changedTraits: Trait[] = [];

    for (let i = 0; i < dataTraits.length; i++) {
        const trait = dataTraits[i];
        if (!hasTrait(world, entity, trait)) continue;

        const store = getStore(world, trait) as any;
        const record = trait[$internal].type === 'soa' ? null : store[eid];
        const keys = dataFields[i];
        let written = false;

        for (let j = 0; j < keys.length; j++) {
            const key = keys[j];
            if (!(key in value)) continue;
            if (record) record[key] = value[key];
            else store[key][eid] = value[key];
            written = true;
        }

        if (written) changedTraits.push(trait);
    }

    // Flag changes only after every field is written so listeners see a consistent aspect.
    if (triggerChanged) {
        for (let i = 0; i < changedTraits.length; i++) setChanged(world, entity, changedTraits[i]);
    }
}

export function addAspect(
    world: World,
    entity: Entity,
    aspect: Aspect,
    params?: Record<string, unknown>
) {
    const traits = aspect.traits;
    const { fields } = aspect[$internal];

    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        if (hasTrait(world, entity, trait)) continue;

        let initial: Record<string, unknown> | undefined;
        if (params) {
            const keys = fields[i];
            for (let j = 0; j < keys.length; j++) {
                if (keys[j] in params) (initial ??= {})[keys[j]] = params[keys[j]];
            }
        }

        if (!initial) {
            addTrait(world, entity, trait);
        } else if (trait[$internal].type === 'aos') {
            // AoS values replace the stored instance, so build a full instance to keep its shape.
            const instance = Object.assign((trait.schema as () => object)(), initial);
            addTrait(world, entity, [trait, instance] as any);
        } else {
            addTrait(world, entity, [trait, initial] as any);
        }
    }
}

export function setAspectChanged(world: World, entity: Entity, aspect: Aspect) {
    const traits = aspect.traits;
    for (let i = 0; i < traits.length; i++) setChanged(world, entity, traits[i]);
}

/**
 * Build the merged view of an aspect from consecutive trait records,
 * as laid out by query results.
 */
export function mergeAspectRecords(aspect: Aspect, records: any[], start: number) {
    const dataFields = aspect[$internal].dataFields;
    const merged: Record<string, unknown> = {};

    for (let i = 0; i < dataFields.length; i++) {
        const record = records[start + i];
        // A callback-based trait the entity lacks has no record, such as when matched through Or.
        if (!record) continue;
        const keys = dataFields[i];
        for (let j = 0; j < keys.length; j++) merged[keys[j]] = record[keys[j]];
    }

    return merged;
}

/**
 * Write the fields of a merged aspect view back into its trait records.
 */
export function splitAspectRecord(aspect: Aspect, merged: any, records: any[], start: number) {
    const dataFields = aspect[$internal].dataFields;

    for (let i = 0; i < dataFields.length; i++) {
        const record = records[start + i];
        if (!record) continue;
        const keys = dataFields[i];
        for (let j = 0; j < keys.length; j++) {
            const key = keys[j];
            if (record[key] !== merged[key]) record[key] = merged[key];
        }
    }
}

type AspectEvent = 'add' | 'remove' | 'change';

function getSubscriptions(instance: TraitInstance, event: AspectEvent) {
    if (event === 'add') return instance.addSubscriptions;
    if (event === 'remove') return instance.removeSubscriptions;
    return instance.changeSubscriptions;
}

/**
 * Subscribe to aspect-level events by listening to every constituent.
 * - add: the entity went from incomplete to complete. Add hooks run after the trait is added.
 * - remove: the entity went from complete to incomplete. Remove hooks run before the trait is removed.
 * - change: a constituent changed while the entity is complete.
 */
export function subscribeAspect(
    world: World,
    aspect: Aspect,
    event: AspectEvent,
    callback: (entity: Entity) => void
) {
    const ctx = world[$internal];

    const instances = aspect.traits.map((trait) => {
        if (!hasTraitInstance(ctx.traitInstances, trait)) registerTrait(world, trait);
        return getTraitInstance(ctx.traitInstances, trait)!;
    });

    const subscriber = (entity: Entity) => {
        if (hasAspect(world, entity, aspect)) callback(entity);
    };

    for (const instance of instances) {
        getSubscriptions(instance, event).add(subscriber);
        if (event === 'change') ctx.trackedTraits.add(instance.trait);
    }

    return () => {
        for (const instance of instances) {
            const subscriptions = getSubscriptions(instance, event);
            subscriptions.delete(subscriber);
            if (event === 'change' && subscriptions.size === 0) {
                ctx.trackedTraits.delete(instance.trait);
            }
        }
    };
}

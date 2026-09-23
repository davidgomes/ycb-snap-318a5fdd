import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import type { QueryUnsubscriber } from '../query/types';
import { setChanged } from '../query/modifiers/changed';
import { isOrderedTrait } from '../relation/ordered';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import { getStore, hasTrait, registerTrait, setTrait } from '../trait/trait';
import { getTraitInstance, hasTraitInstance } from '../trait/trait-instance';
import type { Trait, TraitInstance } from '../trait/types';
import type { World } from '../world';
import { $aspect } from './symbols';
import type {
    Aspect,
    AspectInput,
    AspectInternal,
    AspectRecord,
    AspectValue,
    FlattenAspectInputs,
} from './types';
import { batchAspectChanges, notifyAspectChange } from './utils/change-batch';
import { isAspect } from './utils/is-aspect';

let aspectId = 0;

function isRelationConstituent(input: unknown): boolean {
    if (isRelation(input) || isRelationPair(input)) return true;
    const trait = input as Trait;
    return trait[$internal]?.relation != null || isOrderedTrait(trait);
}

function getTraitFields(trait: Trait): string[] {
    const type = trait[$internal].type;
    if (type === 'soa') return Object.keys(trait.schema);
    if (type === 'aos') {
        const sample = (trait.schema as () => unknown)();
        return sample !== null && typeof sample === 'object' ? Object.keys(sample) : [];
    }
    return [];
}

export function createAspect<T extends AspectInput[]>(...inputs: T): Aspect<FlattenAspectInputs<T>> {
    if (inputs.length < 2) throw new Error('Koota: An aspect requires at least two traits.');

    const traits: Trait[] = [];

    for (const input of inputs) {
        if (isAspect(input)) {
            for (const trait of input.traits) if (!traits.includes(trait)) traits.push(trait);
            continue;
        }

        if (isRelationConstituent(input)) {
            throw new Error('Koota: Relations cannot be used as aspect constituents.');
        }

        if (!traits.includes(input)) traits.push(input);
    }

    const fields: string[][] = [];
    const owners = new Map<string, number>();
    const dataIndices: number[] = [];
    const schema: Record<string, unknown> = {};

    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        const type = trait[$internal].type;
        const keys = getTraitFields(trait);
        const defaults =
            type === 'aos' ? ((trait.schema as () => Record<string, unknown>)() ?? {}) : trait.schema;

        for (const key of keys) {
            if (owners.has(key)) {
                throw new Error(`Koota: Aspect constituents have overlapping field "${key}".`);
            }
            owners.set(key, i);
            schema[key] = defaults[key];
        }

        fields.push(keys);
        if (type !== 'tag') dataIndices.push(i);
    }

    const id = aspectId++;
    const internal: AspectInternal = { id, traits, fields, owners, dataIndices };

    const aspect = Object.assign((params?: AspectValue<any>) => [aspect, params], {
        [$aspect]: true,
        [$internal]: internal,
    }) as unknown as Aspect<FlattenAspectInputs<T>>;

    Object.defineProperty(aspect, 'id', {
        value: id,
        writable: false,
        enumerable: true,
        configurable: false,
    });

    Object.defineProperty(aspect, 'traits', {
        value: Object.freeze(traits.slice()),
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

export function hasAspect(world: World, entity: Entity, aspect: Aspect<any>): boolean {
    const traits = aspect[$internal].traits;
    for (let i = 0; i < traits.length; i++) {
        if (!hasTrait(world, entity, traits[i])) return false;
    }
    return true;
}

export function getAspect<T extends Aspect<any>>(
    world: World,
    entity: Entity,
    aspect: T
): AspectRecord<T> | undefined {
    if (!hasAspect(world, entity, aspect)) return undefined;

    const { traits, fields, dataIndices } = aspect[$internal];
    const eid = getEntityId(entity);
    const result: Record<string, any> = {};

    for (let i = 0; i < dataIndices.length; i++) {
        const index = dataIndices[i];
        const trait = traits[index];
        const record = trait[$internal].get(eid, getStore(world, trait)) as Record<string, any>;
        const keys = fields[index];
        for (let k = 0; k < keys.length; k++) result[keys[k]] = record[keys[k]];
    }

    return result as AspectRecord<T>;
}

export function setAspect(
    world: World,
    entity: Entity,
    aspect: Aspect<any>,
    value: any,
    triggerChanged = true
) {
    if (typeof value === 'function') value = value(getAspect(world, entity, aspect));
    if (!value) return;

    const { traits, fields, dataIndices } = aspect[$internal];
    const eid = getEntityId(entity);

    batchAspectChanges(() => {
        for (let i = 0; i < dataIndices.length; i++) {
            const index = dataIndices[i];
            const trait = traits[index];
            const keys = fields[index];

            let partial: Record<string, any> | undefined;
            for (let k = 0; k < keys.length; k++) {
                const key = keys[k];
                if (!(key in value)) continue;
                partial ??= {};
                partial[key] = value[key];
            }
            if (!partial) continue;

            if (trait[$internal].type === 'aos') {
                // AoS records are object refs, so fields are written onto the existing record.
                if (!hasTrait(world, entity, trait)) continue;
                Object.assign(trait[$internal].get(eid, getStore(world, trait)), partial);
                if (triggerChanged) setChanged(world, entity, trait);
            } else {
                setTrait(world, entity, trait, partial, triggerChanged);
            }
        }
    });
}

export function markAspectChanged(world: World, entity: Entity, aspect: Aspect<any>) {
    const traits = aspect[$internal].traits;
    batchAspectChanges(() => {
        for (let i = 0; i < traits.length; i++) setChanged(world, entity, traits[i]);
    });
}

type AspectSubscriptionType = 'add' | 'remove' | 'change';

const subscriptionKeys = {
    add: 'addSubscriptions',
    remove: 'removeSubscriptions',
    change: 'changeSubscriptions',
} as const;

/**
 * Subscribe to aspect lifecycle events by listening to every constituent.
 * - add: fires when the last missing constituent is added (incomplete -> complete).
 * - remove: fires when the first constituent is removed (complete -> incomplete).
 * - change: fires when any constituent changes while all are present.
 */
export function subscribeAspect(
    world: World,
    aspect: Aspect<any>,
    type: AspectSubscriptionType,
    callback: (entity: Entity) => void
): QueryUnsubscriber {
    const ctx = world[$internal];
    const traits = aspect[$internal].traits;
    const key = subscriptionKeys[type];

    const subscriber =
        type === 'change'
            ? (entity: Entity) => {
                  if (hasAspect(world, entity, aspect)) notifyAspectChange(entity, callback);
              }
            : (entity: Entity) => {
                  if (hasAspect(world, entity, aspect)) callback(entity);
              };

    const instances: TraitInstance[] = [];

    for (const trait of traits) {
        if (!hasTraitInstance(ctx.traitInstances, trait)) registerTrait(world, trait);
        const instance = getTraitInstance(ctx.traitInstances, trait)!;
        instance[key].add(subscriber);
        if (type === 'change') ctx.trackedTraits.add(trait);
        instances.push(instance);
    }

    return () => {
        for (const instance of instances) {
            instance[key].delete(subscriber);
            if (type === 'change' && instance.changeSubscriptions.size === 0) {
                ctx.trackedTraits.delete(instance.trait);
            }
        }
    };
}

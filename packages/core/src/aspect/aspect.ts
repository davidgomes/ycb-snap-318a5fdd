import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { setChanged } from '../query/modifiers/changed';
import { isOrderedTrait } from '../relation/ordered';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import type { Store } from '../storage';
import { addTrait, createTraitId, getStore, hasTrait, removeTrait } from '../trait/trait';
import type { ConfigurableTrait, Trait } from '../trait/types';
import type { World } from '../world';
import { $aspect } from './symbols';
import type { Aspect, AspectInput, FlattenAspectInputs } from './types';
import { isAspect } from './utils/is-aspect';

function getConstituentFields(trait: Trait): string[] {
    const type = trait[$internal].type;
    if (type === 'soa') return Object.keys(trait.schema);
    if (type === 'aos') {
        const sample = (trait.schema as () => unknown)();
        if (sample === null || typeof sample !== 'object') {
            throw new Error('Koota: AoS traits used in an aspect must produce an object.');
        }
        return Object.keys(sample);
    }
    return [];
}

function getConstituentSchema(trait: Trait): Record<string, unknown> {
    const type = trait[$internal].type;
    if (type === 'soa') return trait.schema;
    if (type === 'aos') return (trait.schema as () => Record<string, unknown>)();
    return {};
}

function createAspectFn<T extends AspectInput[]>(...inputs: T): Aspect<FlattenAspectInputs<T>> {
    const traits: Trait[] = [];

    for (const input of inputs) {
        if (isAspect(input)) {
            for (const trait of input.traits) if (!traits.includes(trait)) traits.push(trait);
            continue;
        }

        if (
            isRelation(input) ||
            isRelationPair(input) ||
            (input as Trait)?.[$internal]?.relation ||
            (typeof input === 'function' && isOrderedTrait(input))
        ) {
            throw new Error('Koota: Relations cannot be used as aspect constituents.');
        }

        if (typeof input !== 'function' || !(input as Trait)[$internal]) {
            throw new Error('Koota: Aspects can only be created from traits or other aspects.');
        }

        if (!traits.includes(input)) traits.push(input);
    }

    if (traits.length < 2) {
        throw new Error('Koota: An aspect requires at least two traits.');
    }

    const fields: string[][] = [];
    const owners = new Set<string>();
    const schema: Record<string, unknown> = {};

    for (const trait of traits) {
        const keys = getConstituentFields(trait);
        const traitSchema = getConstituentSchema(trait);

        for (const key of keys) {
            if (owners.has(key)) {
                throw new Error(`Koota: Aspect field "${key}" is defined by more than one trait.`);
            }
            owners.add(key);
            schema[key] = traitSchema[key];
        }

        fields.push(keys);
    }

    const id = createTraitId();
    const aspect = Object.assign((params?: Record<string, unknown>) => [aspect, params], {
        [$internal]: { fields },
    }) as unknown as Aspect<FlattenAspectInputs<T>>;

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

export const createAspect = createAspectFn;

export function hasAspect(world: World, entity: Entity, aspect: Aspect): boolean {
    const traits = aspect.traits;
    for (let i = 0; i < traits.length; i++) {
        if (!hasTrait(world, entity, traits[i])) return false;
    }
    return true;
}

export function getAspectStores(world: World, aspect: Aspect): Store<any>[] {
    return aspect.traits.map((trait) => getStore(world, trait));
}

/** Build a merged snapshot of all constituent fields. Stores are aligned with `aspect.traits`. */
export function readAspect(eid: number, aspect: Aspect, stores: Store<any>[]): Record<string, any> {
    const traits = aspect.traits;
    const fields = aspect[$internal].fields;
    const result: Record<string, any> = {};

    for (let i = 0; i < traits.length; i++) {
        const keys = fields[i];
        if (keys.length === 0) continue;

        const store = stores[i] as any;

        if (traits[i][$internal].type === 'aos') {
            const instance = store[eid];
            for (let j = 0; j < keys.length; j++) result[keys[j]] = instance[keys[j]];
        } else {
            for (let j = 0; j < keys.length; j++) result[keys[j]] = store[keys[j]][eid];
        }
    }

    return result;
}

/**
 * Write every field of a constituent from a merged value back into its store.
 * Returns true if any field value changed.
 */
export function writeAspectConstituent(
    eid: number,
    trait: Trait,
    keys: string[],
    store: any,
    value: Record<string, any>
): boolean {
    let changed = false;

    if (trait[$internal].type === 'aos') {
        const instance = store[eid];
        for (let j = 0; j < keys.length; j++) {
            const key = keys[j];
            if (instance[key] !== value[key]) {
                instance[key] = value[key];
                changed = true;
            }
        }
    } else {
        for (let j = 0; j < keys.length; j++) {
            const key = keys[j];
            if (store[key][eid] !== value[key]) {
                store[key][eid] = value[key];
                changed = true;
            }
        }
    }

    return changed;
}

export function getAspect(world: World, entity: Entity, aspect: Aspect) {
    if (!hasAspect(world, entity, aspect)) return undefined;
    return readAspect(getEntityId(entity), aspect, getAspectStores(world, aspect));
}

export function setAspect(
    world: World,
    entity: Entity,
    aspect: Aspect,
    value: any,
    triggerChanged = true
) {
    value instanceof Function && (value = value(getAspect(world, entity, aspect)));
    if (!value) return;

    const traits = aspect.traits;
    const fields = aspect[$internal].fields;
    const eid = getEntityId(entity);

    beginAspectChangeBatch();

    try {
        for (let i = 0; i < traits.length; i++) {
            const keys = fields[i];
            if (keys.length === 0) continue;

            const trait = traits[i];
            if (!hasTrait(world, entity, trait)) continue;

            let touched = false;
            for (let j = 0; j < keys.length; j++) {
                if (keys[j] in value) {
                    touched = true;
                    break;
                }
            }
            if (!touched) continue;

            const ctx = trait[$internal];
            const store = getStore(world, trait) as any;

            if (ctx.type === 'aos') {
                const instance = store[eid];
                for (let j = 0; j < keys.length; j++) {
                    if (keys[j] in value) instance[keys[j]] = value[keys[j]];
                }
            } else {
                ctx.set(eid, store, value);
            }

            if (triggerChanged) setChanged(world, entity, trait);
        }
    } finally {
        endAspectChangeBatch();
    }
}

export function addAspect(
    world: World,
    entity: Entity,
    aspect: Aspect,
    params?: Record<string, any>
) {
    const traits = aspect.traits;
    const fields = aspect[$internal].fields;
    const configs: ConfigurableTrait[] = [];

    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        if (hasTrait(world, entity, trait)) continue;

        const keys = fields[i];
        let traitParams: Record<string, any> | undefined;

        if (params) {
            for (let j = 0; j < keys.length; j++) {
                const key = keys[j];
                if (key in params) (traitParams ??= {})[key] = params[key];
            }
        }

        if (!traitParams) {
            configs.push(trait);
        } else if (trait[$internal].type === 'aos') {
            const instance = (trait.schema as () => Record<string, any>)();
            configs.push([trait, Object.assign(instance, traitParams)] as ConfigurableTrait);
        } else {
            configs.push([trait, traitParams] as ConfigurableTrait);
        }
    }

    if (configs.length > 0) addTrait(world, entity, ...configs);
}

export function removeAspect(world: World, entity: Entity, aspect: Aspect) {
    removeTrait(world, entity, ...aspect.traits);
}

type AspectHookCallback = (entity: Entity) => void;
type TraitSubscriber = (trait: Trait, callback: AspectHookCallback) => () => void;

/**
 * Subscribe to a per-trait hook for every constituent, only forwarding events
 * that occur while the entity has all constituents.
 */
export function subscribeAspect(
    world: World,
    aspect: Aspect,
    subscribe: TraitSubscriber,
    callback: AspectHookCallback,
    batched = false
): () => void {
    const emit: AspectHookCallback = (entity) => callback(entity);
    const handler: AspectHookCallback = (entity) => {
        if (!hasAspect(world, entity, aspect)) return;
        if (batched && batchDepth > 0) {
            let entities = pendingChanges.get(emit);
            if (!entities) pendingChanges.set(emit, (entities = new Set()));
            entities.add(entity);
            return;
        }
        emit(entity);
    };

    const unsubscribers = aspect.traits.map((trait) => subscribe(trait, handler));

    return () => {
        pendingChanges.delete(emit);
        for (const unsubscribe of unsubscribers) unsubscribe();
    };
}

// Aspect change callbacks are deduplicated per entity while a batch is open so that a
// single write touching several constituents notifies once.
let batchDepth = 0;
const pendingChanges = new Map<AspectHookCallback, Set<Entity>>();

export function beginAspectChangeBatch() {
    batchDepth++;
}

export function endAspectChangeBatch() {
    batchDepth--;
    if (batchDepth > 0 || pendingChanges.size === 0) return;

    const batch = Array.from(pendingChanges);
    pendingChanges.clear();

    for (const [emit, entities] of batch) {
        for (const entity of entities) emit(entity);
    }
}

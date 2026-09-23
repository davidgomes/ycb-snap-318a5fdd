import { $internal } from '../common';
import { isAspect } from '../aspect/aspect';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { isRelationPair } from '../relation/utils/is-relation';
import type { Relation } from '../relation/types';
import { Store } from '../storage';
import { getStore } from '../trait/trait';
import type { Trait } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world';
import { isModifier } from './modifier';
import { setChanged } from './modifiers/changed';
import type {
    InstancesFromParameters,
    ModifierPart,
    QueryInstance,
    QueryParameter,
    QueryResult,
    QueryResultOptions,
    StoresFromParameters,
} from './types';

type DataSlot = {
    merged: boolean;
    traits: Trait[];
    stores: Store<any>[];
};

function queryParamsNeedSlots(params: QueryParameter[]): boolean {
    for (let i = 0; i < params.length; i++) {
        const param = params[i];
        if (isAspect(param)) return true;
        if (isModifier(param) && param.parts) return true;
    }
    return false;
}

function pushTraitSlot(slots: DataSlot[], trait: Trait, world: World) {
    if (trait[$internal].type === 'tag') return;
    slots.push({ merged: false, traits: [trait], stores: [getStore(world, trait)] });
}

function pushAspectSlot(slots: DataSlot[], traits: readonly Trait[], world: World) {
    const dataTraits: Trait[] = [];
    const stores: Store<any>[] = [];
    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        if (trait[$internal].type === 'tag') continue;
        dataTraits.push(trait);
        stores.push(getStore(world, trait));
    }
    slots.push({ merged: true, traits: dataTraits, stores });
}

function buildSlots(params: QueryParameter[], world: World): DataSlot[] {
    const slots: DataSlot[] = [];

    for (let i = 0; i < params.length; i++) {
        const param = params[i];

        if (isRelationPair(param)) {
            const baseTrait = (param[$internal].relation as Relation<Trait>)[$internal].trait;
            pushTraitSlot(slots, baseTrait, world);
            continue;
        }

        if (isAspect(param)) {
            pushAspectSlot(slots, param.traits, world);
            continue;
        }

        if (isModifier(param)) {
            if (param.type === 'not') continue;
            if (param.parts) {
                const parts = param.parts as readonly ModifierPart[];
                for (let j = 0; j < parts.length; j++) {
                    const part = parts[j];
                    if (part.kind === 'trait') pushTraitSlot(slots, part.trait, world);
                    else pushAspectSlot(slots, part.traits, world);
                }
            } else {
                const modifierTraits = param.traits;
                for (let j = 0; j < modifierTraits.length; j++) {
                    pushTraitSlot(slots, modifierTraits[j], world);
                }
            }
            continue;
        }

        pushTraitSlot(slots, param as Trait, world);
    }

    return slots;
}

function readSlot(slot: DataSlot, eid: number): unknown {
    if (!slot.merged) {
        const trait = slot.traits[0];
        return trait[$internal].get(eid, slot.stores[0]);
    }

    const merged: Record<string, unknown> = {};
    for (let i = 0; i < slot.traits.length; i++) {
        const trait = slot.traits[i];
        const value = trait[$internal].get(eid, slot.stores[i]);
        if (value && typeof value === 'object') Object.assign(merged, value);
    }
    return merged;
}

function traitIsTracked(trait: Trait, world: World, query: QueryInstance): boolean {
    return (
        world[$internal].trackedTraits.has(trait) ||
        (query.hasChangedModifiers && query.changedTraits.has(trait))
    );
}

function commitTrait(
    trait: Trait,
    store: Store<any>,
    eid: number,
    value: unknown,
    detect: boolean,
    entity: Entity,
    changedPairs: [Entity, Trait][],
    atomicSnapshot: unknown
) {
    const ctx = trait[$internal];
    if (!detect) {
        ctx.fastSet(eid, store, value);
        return;
    }

    let changed = ctx.fastSetWithChangeDetection(eid, store, value);
    if (!changed && ctx.type === 'aos') changed = !shallowEqual(value, atomicSnapshot);
    if (changed) changedPairs.push([entity, trait]);
}

function commitSlot(
    slot: DataSlot,
    eid: number,
    value: unknown,
    world: World,
    query: QueryInstance,
    entity: Entity,
    changedPairs: [Entity, Trait][],
    mode: 'auto' | 'always' | 'never',
    atomicSnapshot: unknown
) {
    for (let i = 0; i < slot.traits.length; i++) {
        const trait = slot.traits[i];
        const detect = mode === 'always' || (mode === 'auto' && traitIsTracked(trait, world, query));
        commitTrait(trait, slot.stores[i], eid, value, detect, entity, changedPairs, atomicSnapshot);
    }
}

function createSlottedQueryResult<T extends QueryParameter[]>(
    world: World,
    entities: Entity[],
    query: QueryInstance,
    params: QueryParameter[]
): QueryResult<T> {
    let slots = buildSlots(params, world);

    const results = Object.assign(entities, {
        readEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void
        ) {
            const state = Array.from({ length: slots.length }) as InstancesFromParameters<T>;
            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);
                for (let j = 0; j < slots.length; j++) state[j] = readSlot(slots[j], eid) as never;
                callback(state, entity, i);
            }
            return results;
        },

        updateEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void,
            options: QueryResultOptions = { changeDetection: 'auto' }
        ) {
            const mode = options.changeDetection ?? 'auto';
            const state = Array.from({ length: slots.length }) as InstancesFromParameters<T>;
            const atomic = Array.from({ length: slots.length });
            const changedPairs: [Entity, Trait][] = [];

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);
                for (let j = 0; j < slots.length; j++) {
                    const slot = slots[j];
                    const value = readSlot(slot, eid);
                    state[j] = value as never;
                    atomic[j] =
                        !slot.merged && slot.traits[0][$internal].type === 'aos' ? { ...(value as object) } : null;
                }
                callback(state, entity, i);
                if (!world.has(entity)) continue;
                for (let j = 0; j < slots.length; j++) {
                    commitSlot(
                        slots[j],
                        eid,
                        state[j],
                        world,
                        query,
                        entity,
                        changedPairs,
                        mode,
                        atomic[j]
                    );
                }
            }

            for (let i = 0; i < changedPairs.length; i++) {
                const [entity, trait] = changedPairs[i];
                setChanged(world, entity, trait);
            }

            return results;
        },

        useStores(callback: (stores: StoresFromParameters<T>, entities: readonly Entity[]) => void) {
            const stores = slots.map((slot) => (slot.merged ? slot.stores : slot.stores[0]));
            callback(stores as unknown as StoresFromParameters<T>, entities);
            return results;
        },

        select<U extends QueryParameter[]>(...next: U): QueryResult<U> {
            slots = buildSlots(next, world);
            return results as unknown as QueryResult<U>;
        },

        sort(
            callback: (a: Entity, b: Entity) => number = (a, b) => getEntityId(a) - getEntityId(b)
        ): QueryResult<T> {
            Array.prototype.sort.call(entities, callback);
            return results;
        },
    });

    return results;
}

export function createQueryResult<T extends QueryParameter[]>(
    world: World,
    entities: Entity[],
    query: QueryInstance,
    params: QueryParameter[]
): QueryResult<T> {
    if (queryParamsNeedSlots(params)) {
        return createSlottedQueryResult(world, entities, query, params);
    }

    const traits: Trait[] = [];
    const stores: Store<any>[] = [];

    getQueryStores(params, traits, stores, world);

    const results = Object.assign(entities, {
        readEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void
        ) {
            const state = Array.from({ length: traits.length }) as InstancesFromParameters<T>;

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);

                // Create snapshots without atomic tracking
                createSnapshots(eid, traits, stores, state);

                callback(state, entity, i);
            }

            return results;
        },

        updateEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void,
            options: QueryResultOptions = { changeDetection: 'auto' }
        ) {
            const state = Array.from({ length: traits.length });

            // Inline all three permutations of updateEach for performance.
            if (options.changeDetection === 'auto') {
                const changedPairs: [Entity, Trait][] = [];
                const atomicSnapshots: any[] = [];
                const trackedIndices: number[] = [];
                const untrackedIndices: number[] = [];

                getTrackedTraits(traits, world, query, trackedIndices, untrackedIndices);

                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);

                    createSnapshotsWithAtomic(eid, traits, stores, state, atomicSnapshots);
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores for tracked traits.
                    for (let j = 0; j < trackedIndices.length; j++) {
                        const index = trackedIndices[j];
                        const trait = traits[index];
                        const ctx = trait[$internal];
                        const newValue = state[index];
                        const store = stores[index];

                        let changed = false;
                        if (ctx.type === 'aos') {
                            changed = ctx.fastSetWithChangeDetection(eid, store, newValue);
                            if (!changed) {
                                changed = !shallowEqual(newValue, atomicSnapshots[index]);
                            }
                        } else {
                            changed = ctx.fastSetWithChangeDetection(eid, store, newValue);
                        }

                        // Collect changed traits.
                        if (changed) changedPairs.push([entity, trait] as const);
                    }

                    // Commit all changes back to the stores for untracked traits.
                    for (let j = 0; j < untrackedIndices.length; j++) {
                        const index = untrackedIndices[j];
                        const trait = traits[index];
                        const ctx = trait[$internal];
                        const store = stores[index];
                        ctx.fastSet(eid, store, state[index]);
                    }
                }

                // Trigger change events for each entity that was modified.
                for (let i = 0; i < changedPairs.length; i++) {
                    const [entity, trait] = changedPairs[i];
                    setChanged(world, entity, trait);
                }
            } else if (options.changeDetection === 'always') {
                const changedPairs: [Entity, Trait][] = [];
                const atomicSnapshots: any[] = [];

                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);

                    createSnapshotsWithAtomic(eid, traits, stores, state, atomicSnapshots);
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        const trait = traits[j];
                        const ctx = trait[$internal];
                        const newValue = state[j];

                        let changed = false;
                        if (ctx.type === 'aos') {
                            changed = ctx.fastSetWithChangeDetection(eid, stores[j], newValue);
                            if (!changed) {
                                changed = !shallowEqual(newValue, atomicSnapshots[j]);
                            }
                        } else {
                            changed = ctx.fastSetWithChangeDetection(eid, stores[j], newValue);
                        }

                        // Collect changed traits.
                        if (changed) changedPairs.push([entity, trait] as const);
                    }
                }

                // Trigger change events for each entity that was modified.
                for (let i = 0; i < changedPairs.length; i++) {
                    const [entity, trait] = changedPairs[i];
                    setChanged(world, entity, trait);
                }
            } else if (options.changeDetection === 'never') {
                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);
                    createSnapshots(eid, traits, stores, state);
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        const trait = traits[j];
                        const ctx = trait[$internal];
                        ctx.fastSet(eid, stores[j], state[j]);
                    }
                }
            }

            return results;
        },

        useStores(callback: (stores: StoresFromParameters<T>, entities: readonly Entity[]) => void) {
            callback(stores as unknown as StoresFromParameters<T>, entities);
            return results;
        },

        select<U extends QueryParameter[]>(...params: U): QueryResult<U> {
            traits.length = 0;
            stores.length = 0;
            getQueryStores(params, traits, stores, world);
            return results as unknown as QueryResult<U>;
        },

        sort(
            callback: (a: Entity, b: Entity) => number = (a, b) => getEntityId(a) - getEntityId(b)
        ): QueryResult<T> {
            Array.prototype.sort.call(entities, callback);
            return results;
        },
    });

    return results;
}

/* @inline */ function getTrackedTraits(
    traits: Trait[],
    world: World,
    query: QueryInstance,
    trackedIndices: number[],
    untrackedIndices: number[]
) {
    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        const hasTracked = world[$internal].trackedTraits.has(trait);
        const hasChanged = query.hasChangedModifiers && query.changedTraits.has(trait);

        if (hasTracked || hasChanged) trackedIndices.push(i);
        else untrackedIndices.push(i);
    }
}

/* @inline */ function createSnapshots(
    entityId: number,
    traits: Trait[],
    stores: Store<any>[],
    state: any[]
) {
    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        const ctx = trait[$internal];
        const value = ctx.get(entityId, stores[i]);
        state[i] = value;
    }
}

/* @inline */ function createSnapshotsWithAtomic(
    entityId: number,
    traits: Trait[],
    stores: Store<any>[],
    state: any[],
    atomicSnapshots: any[]
) {
    for (let j = 0; j < traits.length; j++) {
        const trait = traits[j];
        const ctx = trait[$internal];
        const value = ctx.get(entityId, stores[j]);
        state[j] = value;
        atomicSnapshots[j] = ctx.type === 'aos' ? { ...value } : null;
    }
}

/* @inline */ export function getQueryStores<T extends QueryParameter[]>(
    params: T,
    traits: Trait[],
    stores: Store<any>[],
    world: World
) {
    for (let i = 0; i < params.length; i++) {
        const param = params[i];

        // Handle relation pairs
        if (isRelationPair(param)) {
            const pairCtx = param[$internal];
            const relation = pairCtx.relation as Relation<Trait>;
            const baseTrait = relation[$internal].trait;
            if (baseTrait[$internal].type !== 'tag') {
                traits.push(baseTrait);
                stores.push(getStore(world, baseTrait));
            }
            continue;
        }

        if (isModifier(param)) {
            // Skip not modifier.
            if (param.type === 'not') continue;

            const modifierTraits = param.traits;
            for (const trait of modifierTraits) {
                if (trait[$internal].type === 'tag') continue; // Skip tags
                traits.push(trait);
                stores.push(getStore(world, trait));
            }
        } else {
            const trait = param as Trait;
            if (trait[$internal].type === 'tag') continue; // Skip tags
            traits.push(trait);
            stores.push(getStore(world, trait));
        }
    }
}

export function createEmptyQueryResult(): QueryResult<QueryParameter[]> {
    const results = Object.assign([], {
        readEach: () => results,
        updateEach: () => results,
        useStores: () => results,
        select: () => results,
        sort: () => results,
    }) as QueryResult<QueryParameter[]>;

    return results;
}

// Cached no-op result methods for relation-only queries
const relationOnlyMethods = {
    readEach(this: QueryResult<any>, callback: any) {
        // No traits to read, just iterate entities
        for (let i = 0; i < this.length; i++) {
            callback([], this[i], i);
        }
        return this;
    },
    updateEach(this: QueryResult<any>, callback: any) {
        // No traits to update, just iterate entities
        for (let i = 0; i < this.length; i++) {
            callback([], this[i], i);
        }
        return this;
    },
    useStores(this: QueryResult<any>, callback: any) {
        // No stores, call with empty array
        callback([], this);
        return this;
    },
    select(this: QueryResult<any>) {
        // No-op, nothing to select
        return this;
    },
};

/**
 * Lightweight query result for relation-only queries.
 * Skips store/trait setup since we only need to iterate entities.
 */
export function createRelationOnlyQueryResult<T extends QueryParameter[]>(
    entities: Entity[]
): QueryResult<T> {
    const results = Object.assign(entities, {
        readEach: relationOnlyMethods.readEach,
        updateEach: relationOnlyMethods.updateEach,
        useStores: relationOnlyMethods.useStores,
        select: relationOnlyMethods.select,
        sort(
            callback: (a: Entity, b: Entity) => number = (a, b) => getEntityId(a) - getEntityId(b)
        ): QueryResult<T> {
            Array.prototype.sort.call(entities, callback);
            return results;
        },
    }) as unknown as QueryResult<T>;

    return results;
}

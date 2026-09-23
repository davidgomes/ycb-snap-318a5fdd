import { batchAspectChanges } from '../aspect/utils/change-batch';
import { isAspect } from '../aspect/utils/is-aspect';
import type { Aspect } from '../aspect/types';
import { $internal } from '../common';
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
    ModifierMember,
    QueryInstance,
    QueryParameter,
    QueryResult,
    QueryResultOptions,
    StoresFromParameters,
} from './types';

export function createQueryResult<T extends QueryParameter[]>(
    world: World,
    entities: Entity[],
    query: QueryInstance,
    params: QueryParameter[]
): QueryResult<T> {
    const traits: Trait[] = [];
    const stores: Store<any>[] = [];
    const layout: QueryLayout = { slots: [], hasAspects: false };

    getQueryStores(params, traits, stores, world, layout);

    const results = Object.assign(entities, {
        readEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void
        ) {
            const state = Array.from({ length: traits.length }) as InstancesFromParameters<T>;
            const output = layout.hasAspects
                ? (Array.from({ length: layout.slots.length }) as InstancesFromParameters<T>)
                : state;

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);

                // Create snapshots without atomic tracking
                createSnapshots(eid, traits, stores, state);
                if (layout.hasAspects) composeOutput(layout, state, output);

                callback(output, entity, i);
            }

            return results;
        },

        updateEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void,
            options: QueryResultOptions = { changeDetection: 'auto' }
        ) {
            const state = Array.from({ length: traits.length });
            const output = layout.hasAspects ? Array.from({ length: layout.slots.length }) : state;

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
                    if (layout.hasAspects) composeOutput(layout, state, output);
                    callback(output as unknown as InstancesFromParameters<T>, entity, i);
                    if (layout.hasAspects) distributeOutput(layout, output, state);

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
                triggerChanged(world, changedPairs);
            } else if (options.changeDetection === 'always') {
                const changedPairs: [Entity, Trait][] = [];
                const atomicSnapshots: any[] = [];

                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);

                    createSnapshotsWithAtomic(eid, traits, stores, state, atomicSnapshots);
                    if (layout.hasAspects) composeOutput(layout, state, output);
                    callback(output as unknown as InstancesFromParameters<T>, entity, i);
                    if (layout.hasAspects) distributeOutput(layout, output, state);

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
                triggerChanged(world, changedPairs);
            } else if (options.changeDetection === 'never') {
                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);
                    createSnapshots(eid, traits, stores, state);
                    if (layout.hasAspects) composeOutput(layout, state, output);
                    callback(output as unknown as InstancesFromParameters<T>, entity, i);
                    if (layout.hasAspects) distributeOutput(layout, output, state);

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
            layout.slots.length = 0;
            layout.hasAspects = false;
            getQueryStores(params, traits, stores, world, layout);
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

/**
 * Maps each slot of the state passed to query callbacks to the flat trait state.
 * A number is the index of a trait, an aspect slot merges several traits into one object.
 */
type AspectSlot = { aspect: Aspect<any>; indices: number[]; fields: string[][] };
type QueryLayout = { slots: (number | AspectSlot)[]; hasAspects: boolean };

function triggerChanged(world: World, changedPairs: [Entity, Trait][]) {
    if (changedPairs.length === 0) return;
    // Batch so aspect change subscribers fire once per entity.
    batchAspectChanges(() => {
        for (let i = 0; i < changedPairs.length; i++) {
            const [entity, trait] = changedPairs[i];
            setChanged(world, entity, trait);
        }
    });
}

/* @inline */ function composeOutput(layout: QueryLayout, state: any[], output: any[]) {
    const slots = layout.slots;
    for (let i = 0; i < slots.length; i++) {
        const slot = slots[i];
        if (typeof slot === 'number') {
            output[i] = state[slot];
            continue;
        }

        const merged: Record<string, any> = {};
        const { indices, fields } = slot;
        for (let j = 0; j < indices.length; j++) {
            const record = state[indices[j]];
            const keys = fields[j];
            for (let k = 0; k < keys.length; k++) merged[keys[k]] = record[keys[k]];
        }
        output[i] = merged;
    }
}

/* @inline */ function distributeOutput(layout: QueryLayout, output: any[], state: any[]) {
    const slots = layout.slots;
    for (let i = 0; i < slots.length; i++) {
        const slot = slots[i];
        if (typeof slot === 'number') {
            state[slot] = output[i];
            continue;
        }

        const merged = output[i];
        const { indices, fields } = slot;
        for (let j = 0; j < indices.length; j++) {
            const record = state[indices[j]];
            const keys = fields[j];
            for (let k = 0; k < keys.length; k++) record[keys[k]] = merged[keys[k]];
        }
    }
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
    world: World,
    layout?: QueryLayout
) {
    const pushTrait = (trait: Trait) => {
        if (trait[$internal].type === 'tag') return; // Skip tags
        layout?.slots.push(traits.length);
        traits.push(trait);
        stores.push(getStore(world, trait));
    };

    const pushAspect = (aspect: Aspect<any>) => {
        const { traits: aspectTraits, fields, dataIndices } = aspect[$internal];
        if (dataIndices.length === 0) return; // Skip aspects made of tags only

        const slot: AspectSlot = { aspect, indices: [], fields: [] };
        for (const index of dataIndices) {
            const trait = aspectTraits[index];
            slot.indices.push(traits.length);
            slot.fields.push(fields[index]);
            traits.push(trait);
            stores.push(getStore(world, trait));
        }

        if (layout) {
            layout.slots.push(slot);
            layout.hasAspects = true;
        }
    };

    for (let i = 0; i < params.length; i++) {
        const param = params[i];

        if (isAspect(param)) {
            pushAspect(param);
            continue;
        }

        // Handle relation pairs
        if (isRelationPair(param)) {
            const pairCtx = param[$internal];
            const relation = pairCtx.relation as Relation<Trait>;
            const baseTrait = relation[$internal].trait;
            pushTrait(baseTrait);
            continue;
        }

        if (isModifier(param)) {
            // Skip not modifier.
            if (param.type === 'not') continue;

            const members: readonly ModifierMember[] = param.members ?? param.traits;
            for (const member of members) {
                if (isAspect(member)) pushAspect(member);
                else pushTrait(member);
            }
        } else {
            pushTrait(param as Trait);
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

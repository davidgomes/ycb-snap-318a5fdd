import {
    beginAspectChangeBatch,
    endAspectChangeBatch,
    getAspectStores,
    readAspect,
    writeAspectConstituent,
} from '../aspect/aspect';
import type { Aspect } from '../aspect/types';
import { isAspect } from '../aspect/utils/is-aspect';
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
    // Holds aspects alongside traits when hasAspects is true. Aspect slots store an
    // array of constituent stores. Paths that assume plain traits only run otherwise.
    const traits: Trait[] = [];
    const stores: Store<any>[] = [];

    let hasAspects = getQueryStores(params, traits, stores, world);

    const results = Object.assign(entities, {
        readEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void
        ) {
            const state = Array.from({ length: traits.length }) as InstancesFromParameters<T>;

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);

                // Create snapshots without atomic tracking
                if (hasAspects) createSnapshotsWithAspects(eid, traits, stores, state);
                else createSnapshots(eid, traits, stores, state);

                callback(state, entity, i);
            }

            return results;
        },

        updateEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void,
            options: QueryResultOptions = { changeDetection: 'auto' }
        ) {
            if (hasAspects) {
                updateEachWithAspects(
                    world,
                    query,
                    entities,
                    traits,
                    stores,
                    callback as (state: any[], entity: Entity, index: number) => void,
                    options.changeDetection ?? 'auto'
                );
                return results;
            }

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
                triggerChanged(world, changedPairs);
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
                triggerChanged(world, changedPairs);
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
            hasAspects = getQueryStores(params, traits, stores, world);
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

function triggerChanged(world: World, changedPairs: [Entity, Trait][]) {
    beginAspectChangeBatch();
    try {
        for (let i = 0; i < changedPairs.length; i++) {
            const [entity, trait] = changedPairs[i];
            setChanged(world, entity, trait);
        }
    } finally {
        endAspectChangeBatch();
    }
}

function createSnapshotsWithAspects(
    entityId: number,
    traits: (Trait | Aspect)[],
    stores: any[],
    state: any[]
) {
    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        state[i] = isAspect(trait)
            ? readAspect(entityId, trait, stores[i])
            : trait[$internal].get(entityId, stores[i]);
    }
}

/**
 * updateEach for results containing aspects. Aspect slots hold a merged object whose
 * fields are distributed back to each constituent store, with per-trait change detection.
 */
function updateEachWithAspects(
    world: World,
    query: QueryInstance,
    entities: Entity[],
    traits: (Trait | Aspect)[],
    stores: any[],
    callback: (state: any[], entity: Entity, index: number) => void,
    changeDetection: 'always' | 'auto' | 'never'
) {
    const state: any[] = Array.from({ length: traits.length });
    const atomicSnapshots: any[] = [];
    const changedPairs: [Entity, Trait][] = [];
    const ctx = world[$internal];

    const isDetected = (trait: Trait) =>
        changeDetection === 'always' ||
        (changeDetection === 'auto' &&
            (ctx.trackedTraits.has(trait) ||
                (query.hasChangedModifiers && query.changedTraits.has(trait))));

    // Per slot: a boolean for traits, a boolean per constituent for aspects.
    const detected = traits.map((trait) =>
        isAspect(trait) ? trait.traits.map(isDetected) : isDetected(trait)
    );

    for (let i = 0; i < entities.length; i++) {
        const entity = entities[i];
        const eid = getEntityId(entity);

        for (let j = 0; j < traits.length; j++) {
            const trait = traits[j];
            if (isAspect(trait)) {
                state[j] = readAspect(eid, trait, stores[j]);
            } else {
                const traitCtx = trait[$internal];
                const value = traitCtx.get(eid, stores[j]);
                state[j] = value;
                atomicSnapshots[j] = traitCtx.type === 'aos' && detected[j] ? { ...value } : null;
            }
        }

        callback(state, entity, i);

        // Skip if the entity has been destroyed.
        if (!world.has(entity)) continue;

        for (let j = 0; j < traits.length; j++) {
            const trait = traits[j];

            if (isAspect(trait)) {
                const constituents = trait.traits;
                const fields = trait[$internal].fields;
                const constituentStores = stores[j];
                const constituentDetected = detected[j] as boolean[];

                for (let k = 0; k < constituents.length; k++) {
                    if (fields[k].length === 0) continue;
                    const changed = writeAspectConstituent(
                        eid,
                        constituents[k],
                        fields[k],
                        constituentStores[k],
                        state[j]
                    );
                    if (changed && constituentDetected[k])
                        changedPairs.push([entity, constituents[k]]);
                }
                continue;
            }

            const traitCtx = trait[$internal];
            const newValue = state[j];

            if (!detected[j]) {
                traitCtx.fastSet(eid, stores[j], newValue);
                continue;
            }

            let changed = traitCtx.fastSetWithChangeDetection(eid, stores[j], newValue);
            if (!changed && traitCtx.type === 'aos') {
                changed = !shallowEqual(newValue, atomicSnapshots[j]);
            }
            if (changed) changedPairs.push([entity, trait]);
        }
    }

    triggerChanged(world, changedPairs);
}

/**
 * Collect the traits and stores for query result data slots.
 * Returns true if any slot is an aspect.
 */
/* @inline */ export function getQueryStores<T extends QueryParameter[]>(
    params: T,
    traits: (Trait | Aspect)[],
    stores: any[],
    world: World
): boolean {
    let hasAspects = false;

    for (let i = 0; i < params.length; i++) {
        const param = params[i];

        if (isAspect(param)) {
            traits.push(param);
            stores.push(getAspectStores(world, param));
            hasAspects = true;
            continue;
        }

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
                if (isAspect(trait)) {
                    traits.push(trait);
                    stores.push(getAspectStores(world, trait));
                    hasAspects = true;
                    continue;
                }
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

    return hasAspects;
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

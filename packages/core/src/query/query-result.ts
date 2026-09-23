import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { getRelationData, setRelationData } from '../relation/relation';
import type { Relation, RelationTarget } from '../relation/types';
import { isRelationPair } from '../relation/utils/is-relation';
import { Store } from '../storage';
import { getStore } from '../trait/trait';
import type { Trait } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world';
import { isModifier, isOrWithModifiers } from './modifier';
import { setChanged, setPairChanged } from './modifiers/changed';
import { getRemovedPairData } from './utils/pair-tracking';
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
    params: QueryParameter[],
    iterationTargets?: Map<Entity, Map<number, Entity>>
): QueryResult<T> {
    const traits: Trait[] = [];
    const stores: Store<any>[] = [];
    const columnTargets: (RelationTarget | undefined)[] = [];

    getQueryStores(params, traits, stores, world, columnTargets);

    const results = Object.assign(entities, {
        readEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void
        ) {
            const state = Array.from({ length: traits.length }) as InstancesFromParameters<T>;

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);

                // Create snapshots without atomic tracking
                createSnapshots(entity, eid, traits, stores, state, world, columnTargets, iterationTargets);

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
                const changedPairs: (readonly [Entity, Trait] | readonly [Entity, Trait, Entity])[] = [];
                const atomicSnapshots: any[] = [];
                const trackedIndices: number[] = [];
                const untrackedIndices: number[] = [];

                getTrackedTraits(traits, world, query, trackedIndices, untrackedIndices);

                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);

                    createSnapshotsWithAtomic(
                        entity,
                        eid,
                        traits,
                        stores,
                        state,
                        atomicSnapshots,
                        world,
                        columnTargets,
                        iterationTargets
                    );
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores for tracked traits.
                    for (let j = 0; j < trackedIndices.length; j++) {
                        const index = trackedIndices[j];
                        const pairTarget = columnTargets[index];
                        if (pairTarget !== undefined) {
                            const target = commitPairColumn(
                                world,
                                entity,
                                traits[index],
                                pairTarget,
                                state[index],
                                atomicSnapshots[index],
                                iterationTargets,
                                true
                            );
                            if (target !== undefined) changedPairs.push([entity, traits[index], target]);
                            continue;
                        }

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
                        const pairTarget = columnTargets[index];
                        if (pairTarget !== undefined) {
                            commitPairColumn(
                                world,
                                entity,
                                traits[index],
                                pairTarget,
                                state[index],
                                atomicSnapshots[index],
                                iterationTargets,
                                false
                            );
                            continue;
                        }
                        const trait = traits[index];
                        const ctx = trait[$internal];
                        const store = stores[index];
                        ctx.fastSet(eid, store, state[index]);
                    }
                }

                // Trigger change events for each entity that was modified.
                signalChangedPairs(world, changedPairs);
            } else if (options.changeDetection === 'always') {
                const changedPairs: (readonly [Entity, Trait] | readonly [Entity, Trait, Entity])[] = [];
                const atomicSnapshots: any[] = [];

                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);

                    createSnapshotsWithAtomic(
                        entity,
                        eid,
                        traits,
                        stores,
                        state,
                        atomicSnapshots,
                        world,
                        columnTargets,
                        iterationTargets
                    );
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        const pairTarget = columnTargets[j];
                        if (pairTarget !== undefined) {
                            const target = commitPairColumn(
                                world,
                                entity,
                                traits[j],
                                pairTarget,
                                state[j],
                                atomicSnapshots[j],
                                iterationTargets,
                                true
                            );
                            if (target !== undefined) changedPairs.push([entity, traits[j], target]);
                            continue;
                        }

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

                signalChangedPairs(world, changedPairs);
            } else if (options.changeDetection === 'never') {
                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);
                    createSnapshots(entity, eid, traits, stores, state, world, columnTargets, iterationTargets);
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        const pairTarget = columnTargets[j];
                        if (pairTarget !== undefined) {
                            commitPairColumn(
                                world,
                                entity,
                                traits[j],
                                pairTarget,
                                state[j],
                                undefined,
                                iterationTargets,
                                false
                            );
                            continue;
                        }
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
            columnTargets.length = 0;
            getQueryStores(params, traits, stores, world, columnTargets);
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

function signalChangedPairs(
    world: World,
    changedPairs: readonly (readonly [Entity, Trait] | readonly [Entity, Trait, Entity])[]
) {
    for (let i = 0; i < changedPairs.length; i++) {
        const change = changedPairs[i];
        if (change.length === 3) setPairChanged(world, change[0], change[1], change[2]);
        else setChanged(world, change[0], change[1]);
    }
}

function resolvePairTarget(
    spec: RelationTarget,
    entity: Entity,
    traitId: number,
    iterationTargets?: Map<Entity, Map<number, Entity>>
): Entity | undefined {
    if (typeof spec === 'number') return spec;
    if (spec === '*') return iterationTargets?.get(entity)?.get(traitId);
    return undefined;
}

function readColumn(
    world: World,
    entity: Entity,
    entityId: number,
    trait: Trait,
    store: Store<any>,
    spec: RelationTarget | undefined,
    iterationTargets?: Map<Entity, Map<number, Entity>>
) {
    if (spec === undefined) return trait[$internal].get(entityId, store);

    const relation = trait[$internal].relation;
    if (!relation) return trait[$internal].get(entityId, store);

    const target = resolvePairTarget(spec, entity, trait.id, iterationTargets);
    if (typeof target !== 'number') return trait[$internal].get(entityId, store);

    const live = getRelationData(world, entity, relation, target);
    if (live !== undefined) return live;
    return getRemovedPairData(world, entity, trait.id, target);
}

/** Writes per-target relation data. Returns the target when a tracked change was committed. */
function commitPairColumn(
    world: World,
    entity: Entity,
    trait: Trait,
    spec: RelationTarget,
    value: unknown,
    previous: unknown,
    iterationTargets: Map<Entity, Map<number, Entity>> | undefined,
    signal: boolean
): Entity | undefined {
    const relation = trait[$internal].relation;
    if (!relation) return undefined;

    const target = resolvePairTarget(spec, entity, trait.id, iterationTargets);
    if (typeof target !== 'number') return undefined;

    const changed = !shallowEqual(value, previous);
    if (signal && !changed) return undefined;

    setRelationData(world, entity, relation, target, (value ?? {}) as Record<string, unknown>);
    return signal && changed ? target : undefined;
}

/* @inline */ function createSnapshots(
    entity: Entity,
    entityId: number,
    traits: Trait[],
    stores: Store<any>[],
    state: any[],
    world: World,
    columnTargets: (RelationTarget | undefined)[],
    iterationTargets?: Map<Entity, Map<number, Entity>>
) {
    for (let i = 0; i < traits.length; i++) {
        state[i] = readColumn(
            world,
            entity,
            entityId,
            traits[i],
            stores[i],
            columnTargets[i],
            iterationTargets
        );
    }
}

/* @inline */ function createSnapshotsWithAtomic(
    entity: Entity,
    entityId: number,
    traits: Trait[],
    stores: Store<any>[],
    state: any[],
    atomicSnapshots: any[],
    world: World,
    columnTargets: (RelationTarget | undefined)[],
    iterationTargets?: Map<Entity, Map<number, Entity>>
) {
    for (let j = 0; j < traits.length; j++) {
        const trait = traits[j];
        const value = readColumn(
            world,
            entity,
            entityId,
            trait,
            stores[j],
            columnTargets[j],
            iterationTargets
        );
        state[j] = value;
        if (columnTargets[j] !== undefined) {
            atomicSnapshots[j] = value && typeof value === 'object' ? { ...value } : value;
        } else {
            atomicSnapshots[j] = trait[$internal].type === 'aos' ? { ...value } : null;
        }
    }
}

function appendModifierColumns(
    modifier: QueryParameter,
    traits: Trait[],
    stores: Store<any>[],
    world: World,
    columnTargets?: (RelationTarget | undefined)[]
) {
    if (!isModifier(modifier) || modifier.type === 'not') return;

    if (isOrWithModifiers(modifier)) {
        const nested = modifier.modifiers;
        for (let i = 0; i < nested.length; i++) {
            appendModifierColumns(nested[i], traits, stores, world, columnTargets);
        }
    }

    const modifierTraits = modifier.traits;
    const pairTargets = modifier.pairTargets;
    for (let j = 0; j < modifierTraits.length; j++) {
        const trait = modifierTraits[j];
        if (trait[$internal].type === 'tag') continue;
        traits.push(trait);
        stores.push(getStore(world, trait));
        columnTargets?.push(pairTargets?.[j]);
    }
}

/* @inline */ export function getQueryStores<T extends QueryParameter[]>(
    params: T,
    traits: Trait[],
    stores: Store<any>[],
    world: World,
    columnTargets?: (RelationTarget | undefined)[]
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
                columnTargets?.push(undefined);
            }
            continue;
        }

        if (isModifier(param)) {
            appendModifierColumns(param, traits, stores, world, columnTargets);
        } else {
            const trait = param as Trait;
            if (trait[$internal].type === 'tag') continue; // Skip tags
            traits.push(trait);
            stores.push(getStore(world, trait));
            columnTargets?.push(undefined);
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

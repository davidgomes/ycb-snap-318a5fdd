import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { getRelationData, setRelationData } from '../relation/relation';
import { isRelationPair } from '../relation/utils/is-relation';
import type { Relation } from '../relation/types';
import { Store } from '../storage';
import { getStore } from '../trait/trait';
import type { Trait } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world';
import { isModifier } from './modifier';
import { setChanged, setPairChanged } from './modifiers/changed';
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
    wildcardTargets?: Map<number, Map<number, number>>
): QueryResult<T> {
    const traits: Trait[] = [];
    const stores: Store<any>[] = [];
    const slotTargets: (number | '*' | undefined)[] = [];
    const slotRelations: (Relation<Trait> | undefined)[] = [];

    getQueryStores(params, traits, stores, world, slotTargets, slotRelations);

    const resolveTarget = (eid: number, index: number): number | undefined => {
        const target = slotTargets[index];
        if (typeof target === 'number') return target;
        if (target === '*') return wildcardTargets?.get(eid)?.get(traits[index].id);
        return undefined;
    };

    const readSlot = (eid: number, index: number) => {
        const relation = slotRelations[index];
        const target = resolveTarget(eid, index);
        if (relation && target !== undefined) {
            return getRelationData(world, eid as Entity, relation, target as Entity);
        }
        return traits[index][$internal].get(eid, stores[index]);
    };

    const writeSlot = (entity: Entity, eid: number, index: number, value: unknown): boolean => {
        const relation = slotRelations[index];
        const target = resolveTarget(eid, index);
        const trait = traits[index];
        if (relation && target !== undefined) {
            const previous = getRelationData(world, entity, relation, target as Entity);
            setRelationData(world, entity, relation, target as Entity, value as Record<string, unknown>);
            return !shallowEqual(value, previous);
        }
        const ctx = trait[$internal];
        return ctx.fastSetWithChangeDetection(eid, stores[index], value);
    };

    const results = Object.assign(entities, {
        readEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void
        ) {
            const state = Array.from({ length: traits.length }) as InstancesFromParameters<T>;

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);

                // Create snapshots without atomic tracking
                createSnapshots(eid, traits, stores, state, readSlot);

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
                const changedPairs: [Entity, Trait, number | undefined][] = [];
                const atomicSnapshots: any[] = [];
                const trackedIndices: number[] = [];
                const untrackedIndices: number[] = [];

                getTrackedTraits(traits, world, query, trackedIndices, untrackedIndices);

                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);

                    createSnapshotsWithAtomic(eid, traits, stores, state, atomicSnapshots, readSlot);
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores for tracked traits.
                    for (let j = 0; j < trackedIndices.length; j++) {
                        const index = trackedIndices[j];
                        const trait = traits[index];
                        const ctx = trait[$internal];
                        const newValue = state[index];

                        let changed = false;
                        if (slotRelations[index]) {
                            changed = writeSlot(entity, eid, index, newValue);
                        } else if (ctx.type === 'aos') {
                            changed = ctx.fastSetWithChangeDetection(eid, stores[index], newValue);
                            if (!changed) {
                                changed = !shallowEqual(newValue, atomicSnapshots[index]);
                            }
                        } else {
                            changed = ctx.fastSetWithChangeDetection(eid, stores[index], newValue);
                        }

                        // Collect changed traits.
                        if (changed) changedPairs.push([entity, trait, resolveTarget(eid, index)] as const);
                    }

                    // Commit all changes back to the stores for untracked traits.
                    for (let j = 0; j < untrackedIndices.length; j++) {
                        const index = untrackedIndices[j];
                        if (slotRelations[index]) writeSlot(entity, eid, index, state[index]);
                        else traits[index][$internal].fastSet(eid, stores[index], state[index]);
                    }
                }

                // Trigger change events for each entity that was modified.
                for (let i = 0; i < changedPairs.length; i++) {
                    const [entity, trait, target] = changedPairs[i];
                    if (target !== undefined) setPairChanged(world, entity, trait, target as Entity);
                    else setChanged(world, entity, trait);
                }
            } else if (options.changeDetection === 'always') {
                const changedPairs: [Entity, Trait, number | undefined][] = [];
                const atomicSnapshots: any[] = [];

                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);

                    createSnapshotsWithAtomic(eid, traits, stores, state, atomicSnapshots, readSlot);
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        const trait = traits[j];
                        const ctx = trait[$internal];
                        const newValue = state[j];

                        let changed = false;
                        if (slotRelations[j]) {
                            changed = writeSlot(entity, eid, j, newValue);
                        } else if (ctx.type === 'aos') {
                            changed = ctx.fastSetWithChangeDetection(eid, stores[j], newValue);
                            if (!changed) {
                                changed = !shallowEqual(newValue, atomicSnapshots[j]);
                            }
                        } else {
                            changed = ctx.fastSetWithChangeDetection(eid, stores[j], newValue);
                        }

                        // Collect changed traits.
                        if (changed) changedPairs.push([entity, trait, resolveTarget(eid, j)] as const);
                    }
                }

                // Trigger change events for each entity that was modified.
                for (let i = 0; i < changedPairs.length; i++) {
                    const [entity, trait, target] = changedPairs[i];
                    if (target !== undefined) setPairChanged(world, entity, trait, target as Entity);
                    else setChanged(world, entity, trait);
                }
            } else if (options.changeDetection === 'never') {
                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);
                    createSnapshots(eid, traits, stores, state, readSlot);
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        if (slotRelations[j]) writeSlot(entity, eid, j, state[j]);
                        else traits[j][$internal].fastSet(eid, stores[j], state[j]);
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
            slotTargets.length = 0;
            slotRelations.length = 0;
            getQueryStores(params, traits, stores, world, slotTargets, slotRelations);
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
    state: any[],
    readSlot?: (eid: number, index: number) => unknown
) {
    for (let i = 0; i < traits.length; i++) {
        state[i] = readSlot
            ? readSlot(entityId, i)
            : traits[i][$internal].get(entityId, stores[i]);
    }
}

/* @inline */ function createSnapshotsWithAtomic(
    entityId: number,
    traits: Trait[],
    stores: Store<any>[],
    state: any[],
    atomicSnapshots: any[],
    readSlot?: (eid: number, index: number) => unknown
) {
    for (let j = 0; j < traits.length; j++) {
        const trait = traits[j];
        const ctx = trait[$internal];
        const value = readSlot ? readSlot(entityId, j) : ctx.get(entityId, stores[j]);
        state[j] = value;
        atomicSnapshots[j] = ctx.type === 'aos' && value && typeof value === 'object' ? { ...value } : null;
    }
}

/* @inline */ export function getQueryStores<T extends QueryParameter[]>(
    params: T,
    traits: Trait[],
    stores: Store<any>[],
    world: World,
    slotTargets?: (number | '*' | undefined)[],
    slotRelations?: (Relation<Trait> | undefined)[]
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
                slotTargets?.push(undefined);
                slotRelations?.push(undefined);
            }
            continue;
        }

        if (isModifier(param)) {
            // Skip not modifier.
            if (param.type === 'not') continue;

            const pairs = param.pairs;
            if (pairs && pairs.length > 0) {
                for (let p = 0; p < pairs.length; p++) {
                    const pair = pairs[p];
                    if (pair.trait[$internal].type === 'tag') continue;
                    traits.push(pair.trait);
                    stores.push(getStore(world, pair.trait));
                    slotTargets?.push(pair.target === '*' ? '*' : (pair.target as number));
                    slotRelations?.push(pair.relation);
                }
            }

            const paired = new Set<number>();
            if (pairs) {
                for (let p = 0; p < pairs.length; p++) paired.add(pairs[p].trait.id);
            }

            const modifierTraits = param.traits;
            for (const trait of modifierTraits) {
                if (paired.has(trait.id)) continue;
                if (trait[$internal].type === 'tag') continue; // Skip tags
                traits.push(trait);
                stores.push(getStore(world, trait));
                slotTargets?.push(undefined);
                slotRelations?.push(undefined);
            }
        } else {
            const trait = param as Trait;
            if (trait[$internal].type === 'tag') continue; // Skip tags
            traits.push(trait);
            stores.push(getStore(world, trait));
            slotTargets?.push(undefined);
            slotRelations?.push(undefined);
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

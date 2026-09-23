import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { getRelationData, hasRelationToTarget, setRelationData } from '../relation/relation';
import { isRelationPair } from '../relation/utils/is-relation';
import type { Relation, RelationTarget } from '../relation/types';
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
import { snapshotKey } from './utils/pair-tracking';

type PairSlot = {
    relation: Relation<Trait>;
    trackingId: number;
    relationTraitId: number;
    target: RelationTarget;
};

export function createQueryResult<T extends QueryParameter[]>(
    world: World,
    entities: Entity[],
    query: QueryInstance,
    params: QueryParameter[]
): QueryResult<T> {
    const traits: Trait[] = [];
    const stores: Store<any>[] = [];
    const pairSlots: (PairSlot | undefined)[] = [];

    getQueryStores(params, traits, stores, world, pairSlots);

    function readValue(entity: Entity, index: number) {
        const slot = pairSlots[index];
        const trait = traits[index];
        if (!slot) return trait[$internal].get(getEntityId(entity), stores[index]);

        const snap = query.pairSnapshots.get(
            snapshotKey(entity, slot.trackingId, slot.relationTraitId, slot.target)
        );
        if (snap?.removed) return snap.removedData;

        const target = snap?.target ?? (typeof slot.target === 'number' ? slot.target : undefined);
        if (typeof target !== 'number') return undefined;
        return getRelationData(world, entity, slot.relation, target);
    }

    function writePair(entity: Entity, index: number, value: unknown) {
        const slot = pairSlots[index];
        if (!slot) return false;

        const snap = query.pairSnapshots.get(
            snapshotKey(entity, slot.trackingId, slot.relationTraitId, slot.target)
        );
        if (snap?.removed) return false;

        const target = snap?.target ?? (typeof slot.target === 'number' ? slot.target : undefined);
        if (typeof target !== 'number') return false;
        if (!hasRelationToTarget(world, slot.relation, entity, target)) return false;

        setRelationData(world, entity, slot.relation, target, value as Record<string, unknown>);
        return true;
    }

    function pairTarget(entity: Entity, slot: PairSlot) {
        const snap = query.pairSnapshots.get(
            snapshotKey(entity, slot.trackingId, slot.relationTraitId, slot.target)
        );
        const target = snap?.target ?? (typeof slot.target === 'number' ? slot.target : undefined);
        return typeof target === 'number' ? target : undefined;
    }

    function fillSnapshots(entity: Entity, state: any[], atomicSnapshots?: any[]) {
        for (let j = 0; j < traits.length; j++) {
            const value = readValue(entity, j);
            state[j] = value;
            if (!atomicSnapshots) continue;
            if (pairSlots[j]) {
                atomicSnapshots[j] = value && typeof value === 'object' ? { ...value } : value;
            } else if (traits[j][$internal].type === 'aos') {
                atomicSnapshots[j] = value ? { ...value } : value;
            } else {
                atomicSnapshots[j] = null;
            }
        }
    }

    const results = Object.assign(entities, {
        readEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void
        ) {
            const state = Array.from({ length: traits.length }) as InstancesFromParameters<T>;

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                fillSnapshots(entity, state);
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
                const changedPairTargets: [Entity, Trait, Entity][] = [];
                const atomicSnapshots: any[] = [];
                const trackedIndices: number[] = [];
                const untrackedIndices: number[] = [];

                getTrackedTraits(traits, world, query, trackedIndices, untrackedIndices);

                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);

                    fillSnapshots(entity, state, atomicSnapshots);
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores for tracked traits.
                    for (let j = 0; j < trackedIndices.length; j++) {
                        const index = trackedIndices[j];
                        const trait = traits[index];
                        const newValue = state[index];

                        if (pairSlots[index]) {
                            if (!shallowEqual(newValue, atomicSnapshots[index])) {
                                if (writePair(entity, index, newValue)) {
                                    const target = pairTarget(entity, pairSlots[index]!);
                                    if (typeof target === 'number') {
                                        changedPairTargets.push([entity, trait, target]);
                                    }
                                }
                            }
                            continue;
                        }

                        const ctx = trait[$internal];
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
                        if (pairSlots[index]) {
                            writePair(entity, index, state[index]);
                            continue;
                        }
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
                for (let i = 0; i < changedPairTargets.length; i++) {
                    const [entity, trait, target] = changedPairTargets[i];
                    setPairChanged(world, entity, trait, target);
                }
            } else if (options.changeDetection === 'always') {
                const changedPairs: [Entity, Trait][] = [];
                const changedPairTargets: [Entity, Trait, Entity][] = [];
                const atomicSnapshots: any[] = [];

                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);

                    fillSnapshots(entity, state, atomicSnapshots);
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        const trait = traits[j];
                        const newValue = state[j];

                        if (pairSlots[j]) {
                            if (!shallowEqual(newValue, atomicSnapshots[j]) && writePair(entity, j, newValue)) {
                                const target = pairTarget(entity, pairSlots[j]!);
                                if (typeof target === 'number') changedPairTargets.push([entity, trait, target]);
                            }
                            continue;
                        }

                        const ctx = trait[$internal];

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
                for (let i = 0; i < changedPairTargets.length; i++) {
                    const [entity, trait, target] = changedPairTargets[i];
                    setPairChanged(world, entity, trait, target);
                }
            } else if (options.changeDetection === 'never') {
                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);
                    fillSnapshots(entity, state);
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        if (pairSlots[j]) {
                            writePair(entity, j, state[j]);
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
            pairSlots.length = 0;
            getQueryStores(params, traits, stores, world, pairSlots);
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

/* @inline */ export function getQueryStores<T extends QueryParameter[]>(
    params: T,
    traits: Trait[],
    stores: Store<any>[],
    world: World,
    pairSlots?: (PairSlot | undefined)[]
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
                pairSlots?.push(undefined);
            }
            continue;
        }

        if (isModifier(param)) {
            // Skip not modifier.
            if (param.type === 'not') continue;

            const modifierTraits = param.traits;
            let pairCursor = 0;
            for (let j = 0; j < modifierTraits.length; j++) {
                const trait = modifierTraits[j];
                const isPair = param.pairMask?.[j] === true;
                const pair = isPair ? param.pairs?.[pairCursor++] : undefined;
                if (trait[$internal].type === 'tag') continue;
                traits.push(trait);
                stores.push(getStore(world, trait));
                if (pair) {
                    const relation = pair[$internal].relation as Relation<Trait>;
                    pairSlots?.push({
                        relation,
                        trackingId: param.id,
                        relationTraitId: relation[$internal].trait.id,
                        target: pair[$internal].target,
                    });
                } else {
                    pairSlots?.push(undefined);
                }
            }
        } else {
            const trait = param as Trait;
            if (trait[$internal].type === 'tag') continue; // Skip tags
            traits.push(trait);
            stores.push(getStore(world, trait));
            pairSlots?.push(undefined);
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

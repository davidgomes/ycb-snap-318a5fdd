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
import { getPairKey, type PairMatch, type PairMatches } from './utils/pair-tracking';
import type {
    InstancesFromParameters,
    QueryInstance,
    QueryParameter,
    QueryResult,
    QueryResultOptions,
    StoresFromParameters,
} from './types';

/** A relation pair tracked by a modifier, with the pairs its query matched */
type PairTarget = {
    target: RelationTarget;
    matches: Map<Entity, PairMatch> | undefined;
};

export function createQueryResult<T extends QueryParameter[]>(
    world: World,
    entities: Entity[],
    query: QueryInstance,
    params: QueryParameter[],
    pairMatches?: PairMatches
): QueryResult<T> {
    const traits: Trait[] = [];
    const stores: Store<any>[] = [];
    const pairTargets: (PairTarget | undefined)[] = [];

    getQueryStores(params, traits, stores, world, pairTargets, pairMatches);

    const results = Object.assign(entities, {
        readEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void
        ) {
            const state = Array.from({ length: traits.length }) as InstancesFromParameters<T>;

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);

                // Create snapshots without atomic tracking
                createSnapshots(world, entity, eid, traits, stores, pairTargets, state);

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
                const changedPairs: [Entity, Trait, Entity | undefined][] = [];
                const atomicSnapshots: any[] = [];
                const trackedIndices: number[] = [];
                const untrackedIndices: number[] = [];

                getTrackedTraits(traits, world, query, trackedIndices, untrackedIndices);

                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);

                    createSnapshotsWithAtomic(
                        world,
                        entity,
                        eid,
                        traits,
                        stores,
                        pairTargets,
                        state,
                        atomicSnapshots
                    );
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
                        const pairTarget = pairTargets[index];

                        if (pairTarget !== undefined) {
                            const target = commitPairState(
                                world,
                                entity,
                                trait,
                                pairTarget,
                                newValue,
                                atomicSnapshots[index]
                            );
                            if (target !== undefined) changedPairs.push([entity, trait, target]);
                            continue;
                        }

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
                        if (changed) changedPairs.push([entity, trait, undefined]);
                    }

                    // Commit all changes back to the stores for untracked traits.
                    for (let j = 0; j < untrackedIndices.length; j++) {
                        const index = untrackedIndices[j];
                        const trait = traits[index];
                        const ctx = trait[$internal];
                        const store = stores[index];
                        const pairTarget = pairTargets[index];

                        if (pairTarget !== undefined) {
                            writePairState(world, entity, trait, pairTarget, state[index]);
                            continue;
                        }

                        ctx.fastSet(eid, store, state[index]);
                    }
                }

                // Trigger change events for each entity that was modified.
                triggerChanges(world, changedPairs);
            } else if (options.changeDetection === 'always') {
                const changedPairs: [Entity, Trait, Entity | undefined][] = [];
                const atomicSnapshots: any[] = [];

                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);

                    createSnapshotsWithAtomic(
                        world,
                        entity,
                        eid,
                        traits,
                        stores,
                        pairTargets,
                        state,
                        atomicSnapshots
                    );
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        const trait = traits[j];
                        const ctx = trait[$internal];
                        const newValue = state[j];
                        const pairTarget = pairTargets[j];

                        if (pairTarget !== undefined) {
                            const target = commitPairState(
                                world,
                                entity,
                                trait,
                                pairTarget,
                                newValue,
                                atomicSnapshots[j]
                            );
                            if (target !== undefined) changedPairs.push([entity, trait, target]);
                            continue;
                        }

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
                        if (changed) changedPairs.push([entity, trait, undefined]);
                    }
                }

                // Trigger change events for each entity that was modified.
                triggerChanges(world, changedPairs);
            } else if (options.changeDetection === 'never') {
                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);
                    createSnapshots(world, entity, eid, traits, stores, pairTargets, state);
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        const trait = traits[j];
                        const ctx = trait[$internal];
                        const pairTarget = pairTargets[j];

                        if (pairTarget !== undefined) {
                            writePairState(world, entity, trait, pairTarget, state[j]);
                            continue;
                        }

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
            pairTargets.length = 0;
            getQueryStores(params, traits, stores, world, pairTargets, pairMatches);
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
    world: World,
    entity: Entity,
    entityId: number,
    traits: Trait[],
    stores: Store<any>[],
    pairTargets: (PairTarget | undefined)[],
    state: any[]
) {
    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        const ctx = trait[$internal];
        const pairTarget = pairTargets[i];
        const value =
            pairTarget === undefined
                ? ctx.get(entityId, stores[i])
                : readPairState(world, entity, trait, pairTarget);
        state[i] = value;
    }
}

/* @inline */ function createSnapshotsWithAtomic(
    world: World,
    entity: Entity,
    entityId: number,
    traits: Trait[],
    stores: Store<any>[],
    pairTargets: (PairTarget | undefined)[],
    state: any[],
    atomicSnapshots: any[]
) {
    for (let j = 0; j < traits.length; j++) {
        const trait = traits[j];
        const ctx = trait[$internal];
        const pairTarget = pairTargets[j];

        if (pairTarget !== undefined) {
            const value = readPairState(world, entity, trait, pairTarget);
            state[j] = value;
            atomicSnapshots[j] = value === undefined ? undefined : { ...value };
            continue;
        }

        const value = ctx.get(entityId, stores[j]);
        state[j] = value;
        atomicSnapshots[j] = ctx.type === 'aos' ? { ...value } : null;
    }
}

/* @inline */ function resolvePairTarget(pairTarget: PairTarget, entity: Entity) {
    const target = pairTarget.target;
    return target === '*' ? pairTarget.matches?.get(entity)?.target : target;
}

/* @inline */ function readPairState(
    world: World,
    entity: Entity,
    trait: Trait,
    pairTarget: PairTarget
): any {
    const target = resolvePairTarget(pairTarget, entity);
    if (target === undefined) return undefined;

    const data = getRelationData(world, entity, trait[$internal].relation!, target);
    if (data !== undefined) return data;

    // The pair is gone, so use its data from when it was removed if it was tracked as removed.
    const match = pairTarget.matches?.get(entity);
    return match?.target === target ? match.data : undefined;
}

/** Write pair data back to its target and return the target, unless the pair is gone. */
/* @inline */ function writePairState(
    world: World,
    entity: Entity,
    trait: Trait,
    pairTarget: PairTarget,
    value: any
): Entity | undefined {
    const target = resolvePairTarget(pairTarget, entity);
    if (target === undefined || value === undefined) return undefined;

    const relation = trait[$internal].relation!;
    if (!hasRelationToTarget(world, relation, entity, target)) return undefined;

    setRelationData(world, entity, relation, target, value);
    return target;
}

/** Write pair data back to its target and return the target if the data changed. */
/* @inline */ function commitPairState(
    world: World,
    entity: Entity,
    trait: Trait,
    pairTarget: PairTarget,
    value: any,
    snapshot: any
): Entity | undefined {
    const target = writePairState(world, entity, trait, pairTarget, value);
    return target !== undefined && !shallowEqual(value, snapshot) ? target : undefined;
}

/* @inline */ function triggerChanges(world: World, changed: [Entity, Trait, Entity | undefined][]) {
    for (let i = 0; i < changed.length; i++) {
        const [entity, trait, target] = changed[i];
        if (target === undefined) setChanged(world, entity, trait);
        else setPairChanged(world, entity, trait, target);
    }
}

/**
 * Collect the traits and stores to iterate for the parameters. Relation pairs tracked
 * by a modifier are resolved to the data of their target, which is recorded at the same
 * index in `pairTargets`. For `'*'` pairs it is the first target that satisfied the modifier.
 */
/* @inline */ export function getQueryStores<T extends QueryParameter[]>(
    params: T,
    traits: Trait[],
    stores: Store<any>[],
    world: World,
    pairTargets: (PairTarget | undefined)[],
    pairMatches?: PairMatches
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
            const pairs = param.pairs;
            for (let j = 0; j < modifierTraits.length; j++) {
                const trait = modifierTraits[j];
                if (trait[$internal].type === 'tag') continue; // Skip tags
                const pair = pairs?.[j];
                if (pair) {
                    const target = pair[$internal].target;
                    pairTargets[traits.length] = {
                        target,
                        matches: pairMatches?.get(getPairKey(param.id, trait.id, target)),
                    };
                }
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

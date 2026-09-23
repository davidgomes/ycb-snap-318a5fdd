import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { getFirstRelationTarget, getRelationData, setRelationData } from '../relation/relation';
import type { Relation, RelationPair, RelationTarget } from '../relation/types';
import { isRelationPair } from '../relation/utils/is-relation';
import { Store } from '../storage';
import { getStore } from '../trait/trait';
import type { Trait } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world';
import { isModifier, isOrWithModifiers } from './modifier';
import { setChanged, setPairChanged } from './modifiers/changed';
import type {
    InstancesFromParameters,
    Modifier,
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
    wildcardTargets: Map<Entity, Map<number, Entity>> | null = null
): QueryResult<T> {
    const traits: Trait[] = [];
    const stores: Store<any>[] = [];
    const relations: (Relation<Trait> | undefined)[] = [];
    const targets: (RelationTarget | undefined)[] = [];

    getQueryStores(params, traits, stores, world, relations, targets);

    const results = Object.assign(entities, {
        readEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void
        ) {
            const state = Array.from({ length: traits.length }) as InstancesFromParameters<T>;

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);

                // Create snapshots without atomic tracking
                createSnapshots(
                    world,
                    entity,
                    eid,
                    traits,
                    stores,
                    relations,
                    targets,
                    wildcardTargets,
                    state
                );

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
                        relations,
                        targets,
                        wildcardTargets,
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
                        const newValue = state[index];

                        if (relations[index]) {
                            const target = resolvePairTarget(
                                world,
                                entity,
                                trait,
                                relations[index]!,
                                targets[index],
                                wildcardTargets
                            );
                            if (
                                target !== undefined &&
                                !shallowEqual(newValue, atomicSnapshots[index])
                            ) {
                                setRelationData(world, entity, relations[index]!, target, newValue);
                                changedPairs.push([entity, trait, target]);
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
                        if (changed) changedPairs.push([entity, trait, undefined]);
                    }

                    // Commit all changes back to the stores for untracked traits.
                    for (let j = 0; j < untrackedIndices.length; j++) {
                        const index = untrackedIndices[j];
                        const trait = traits[index];
                        if (relations[index]) {
                            const target = resolvePairTarget(
                                world,
                                entity,
                                trait,
                                relations[index]!,
                                targets[index],
                                wildcardTargets
                            );
                            if (
                                target !== undefined &&
                                !shallowEqual(state[index], atomicSnapshots[index])
                            ) {
                                setRelationData(world, entity, relations[index]!, target, state[index]);
                            }
                            continue;
                        }
                        const ctx = trait[$internal];
                        const store = stores[index];
                        ctx.fastSet(eid, store, state[index]);
                    }
                }

                // Trigger change events for each entity that was modified.
                for (let i = 0; i < changedPairs.length; i++) {
                    const [entity, trait, target] = changedPairs[i];
                    if (target !== undefined) setPairChanged(world, entity, trait, target);
                    else setChanged(world, entity, trait);
                }
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
                        relations,
                        targets,
                        wildcardTargets,
                        state,
                        atomicSnapshots
                    );
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        const trait = traits[j];
                        const newValue = state[j];

                        if (relations[j]) {
                            const target = resolvePairTarget(
                                world,
                                entity,
                                trait,
                                relations[j]!,
                                targets[j],
                                wildcardTargets
                            );
                            if (
                                target !== undefined &&
                                !shallowEqual(newValue, atomicSnapshots[j])
                            ) {
                                setRelationData(world, entity, relations[j]!, target, newValue);
                                changedPairs.push([entity, trait, target]);
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
                        if (changed) changedPairs.push([entity, trait, undefined]);
                    }
                }

                // Trigger change events for each entity that was modified.
                for (let i = 0; i < changedPairs.length; i++) {
                    const [entity, trait, target] = changedPairs[i];
                    if (target !== undefined) setPairChanged(world, entity, trait, target);
                    else setChanged(world, entity, trait);
                }
            } else if (options.changeDetection === 'never') {
                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);
                    createSnapshots(
                        world,
                        entity,
                        eid,
                        traits,
                        stores,
                        relations,
                        targets,
                        wildcardTargets,
                        state
                    );
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        const trait = traits[j];
                        if (relations[j]) {
                            const target = resolvePairTarget(
                                world,
                                entity,
                                trait,
                                relations[j]!,
                                targets[j],
                                wildcardTargets
                            );
                            if (target !== undefined) {
                                setRelationData(world, entity, relations[j]!, target, state[j]);
                            }
                            continue;
                        }
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
            relations.length = 0;
            targets.length = 0;
            getQueryStores(params, traits, stores, world, relations, targets);
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

function resolvePairTarget(
    world: World,
    entity: Entity,
    trait: Trait,
    relation: Relation<Trait>,
    target: RelationTarget | undefined,
    wildcardTargets: Map<Entity, Map<number, Entity>> | null
): Entity | undefined {
    if (target === undefined) return undefined;
    if (target !== '*') return target;
    const captured = wildcardTargets?.get(entity)?.get(trait.id);
    if (captured !== undefined) return captured;
    return getFirstRelationTarget(world, relation, entity);
}

function readColumn(
    world: World,
    entity: Entity,
    entityId: number,
    index: number,
    traits: Trait[],
    stores: Store<any>[],
    relations: (Relation<Trait> | undefined)[],
    targets: (RelationTarget | undefined)[],
    wildcardTargets: Map<Entity, Map<number, Entity>> | null
) {
    const relation = relations[index];
    if (relation) {
        const target = resolvePairTarget(
            world,
            entity,
            traits[index],
            relation,
            targets[index],
            wildcardTargets
        );
        if (target === undefined) return undefined;
        return getRelationData(world, entity, relation, target);
    }

    return traits[index][$internal].get(entityId, stores[index]);
}

/* @inline */ function createSnapshots(
    world: World,
    entity: Entity,
    entityId: number,
    traits: Trait[],
    stores: Store<any>[],
    relations: (Relation<Trait> | undefined)[],
    targets: (RelationTarget | undefined)[],
    wildcardTargets: Map<Entity, Map<number, Entity>> | null,
    state: any[]
) {
    for (let i = 0; i < traits.length; i++) {
        state[i] = readColumn(
            world,
            entity,
            entityId,
            i,
            traits,
            stores,
            relations,
            targets,
            wildcardTargets
        );
    }
}

/* @inline */ function createSnapshotsWithAtomic(
    world: World,
    entity: Entity,
    entityId: number,
    traits: Trait[],
    stores: Store<any>[],
    relations: (Relation<Trait> | undefined)[],
    targets: (RelationTarget | undefined)[],
    wildcardTargets: Map<Entity, Map<number, Entity>> | null,
    state: any[],
    atomicSnapshots: any[]
) {
    for (let j = 0; j < traits.length; j++) {
        const value = readColumn(
            world,
            entity,
            entityId,
            j,
            traits,
            stores,
            relations,
            targets,
            wildcardTargets
        );
        state[j] = value;
        const ctx = traits[j][$internal];
        atomicSnapshots[j] =
            relations[j] || ctx.type === 'aos'
                ? value && typeof value === 'object'
                    ? { ...value }
                    : value
                : null;
    }
}

function pushTraitColumn(
    world: World,
    trait: Trait,
    traits: Trait[],
    stores: Store<any>[],
    relations: (Relation<Trait> | undefined)[],
    targets: (RelationTarget | undefined)[],
    relation?: Relation<Trait>,
    target?: RelationTarget
) {
    if (trait[$internal].type === 'tag') return;
    traits.push(trait);
    stores.push(getStore(world, trait));
    relations.push(relation);
    targets.push(target);
}

function pushModifierColumns(
    world: World,
    modifier: Modifier,
    traits: Trait[],
    stores: Store<any>[],
    relations: (Relation<Trait> | undefined)[],
    targets: (RelationTarget | undefined)[],
    includePlainTraits: boolean
) {
    if (modifier.type === 'not') return;

    const pairs = modifier.pairs;
    const pairQueue: RelationPair[] = pairs ? pairs.slice() : [];

    if (includePlainTraits || pairQueue.length > 0) {
        const modifierTraits = modifier.traits as Trait[];
        for (let i = 0; i < modifierTraits.length; i++) {
            const trait = modifierTraits[i];
            const pairIndex = pairQueue.findIndex(
                (pair) => pair[$internal].relation[$internal].trait === trait
            );
            if (pairIndex !== -1) {
                const pair = pairQueue.splice(pairIndex, 1)[0];
                const relation = pair[$internal].relation as Relation<Trait>;
                pushTraitColumn(
                    world,
                    trait,
                    traits,
                    stores,
                    relations,
                    targets,
                    relation,
                    pair[$internal].target
                );
                continue;
            }
            if (!includePlainTraits) continue;
            pushTraitColumn(world, trait, traits, stores, relations, targets);
        }
    }

    if (isOrWithModifiers(modifier)) {
        for (let i = 0; i < modifier.modifiers.length; i++) {
            // Nested modifiers contribute pair columns only, matching prior trait iteration.
            pushModifierColumns(
                world,
                modifier.modifiers[i],
                traits,
                stores,
                relations,
                targets,
                false
            );
        }
    }
}

/* @inline */ export function getQueryStores<T extends QueryParameter[]>(
    params: T,
    traits: Trait[],
    stores: Store<any>[],
    world: World,
    relations: (Relation<Trait> | undefined)[],
    targets: (RelationTarget | undefined)[]
) {
    for (let i = 0; i < params.length; i++) {
        const param = params[i];

        // Handle relation pairs
        if (isRelationPair(param)) {
            const pairCtx = param[$internal];
            const relation = pairCtx.relation as Relation<Trait>;
            const baseTrait = relation[$internal].trait;
            pushTraitColumn(
                world,
                baseTrait,
                traits,
                stores,
                relations,
                targets,
                relation,
                pairCtx.target
            );
            continue;
        }

        if (isModifier(param)) {
            pushModifierColumns(world, param, traits, stores, relations, targets, true);
        } else {
            pushTraitColumn(world, param as Trait, traits, stores, relations, targets);
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

import { ensureAspect } from '../aspect/create-aspect';
import { isAspect } from '../aspect/is-aspect';
import type { Aspect } from '../aspect/types';
import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { isRelationPair } from '../relation/utils/is-relation';
import type { Relation } from '../relation/types';
import { Store } from '../storage';
import { getStore, registerTrait } from '../trait/trait';
import { hasTraitInstance } from '../trait/trait-instance';
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
    const traits: Trait[] = [];
    const stores: Store<any>[] = [];
    const aspectColumns: (AspectColumn | undefined)[] = [];

    getQueryStores(params, traits, stores, world, aspectColumns);

    const results = Object.assign(entities, {
        readEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void
        ) {
            const state = Array.from({ length: traits.length }) as InstancesFromParameters<T>;

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);

                // Create snapshots without atomic tracking
                createSnapshots(eid, traits, stores, state, aspectColumns);

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

                getTrackedTraits(
                    traits,
                    world,
                    query,
                    trackedIndices,
                    untrackedIndices,
                    aspectColumns
                );

                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);

                    createSnapshotsWithAtomic(
                        eid,
                        traits,
                        stores,
                        state,
                        atomicSnapshots,
                        aspectColumns
                    );
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores for tracked traits.
                    for (let j = 0; j < trackedIndices.length; j++) {
                        const index = trackedIndices[j];
                        const aspectColumn = aspectColumns[index];
                        if (aspectColumn) {
                            const changed = writeAspectColumn(aspectColumn, eid, state[index], true);
                            for (let k = 0; k < changed.length; k++) {
                                changedPairs.push([entity, changed[k]]);
                            }
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
                        const aspectColumn = aspectColumns[index];
                        if (aspectColumn) {
                            writeAspectColumn(aspectColumn, eid, state[index], false);
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
            } else if (options.changeDetection === 'always') {
                const changedPairs: [Entity, Trait][] = [];
                const atomicSnapshots: any[] = [];

                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);

                    createSnapshotsWithAtomic(
                        eid,
                        traits,
                        stores,
                        state,
                        atomicSnapshots,
                        aspectColumns
                    );
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        const aspectColumn = aspectColumns[j];
                        if (aspectColumn) {
                            const changed = writeAspectColumn(aspectColumn, eid, state[j], true);
                            for (let k = 0; k < changed.length; k++) {
                                changedPairs.push([entity, changed[k]]);
                            }
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

                // Trigger change events for each entity that was modified.
                for (let i = 0; i < changedPairs.length; i++) {
                    const [entity, trait] = changedPairs[i];
                    setChanged(world, entity, trait);
                }
            } else if (options.changeDetection === 'never') {
                for (let i = 0; i < entities.length; i++) {
                    const entity = entities[i];
                    const eid = getEntityId(entity);
                    createSnapshots(eid, traits, stores, state, aspectColumns);
                    callback(state as unknown as InstancesFromParameters<T>, entity, i);

                    // Skip if the entity has been destroyed.
                    if (!world.has(entity)) continue;

                    // Commit all changes back to the stores.
                    for (let j = 0; j < traits.length; j++) {
                        const aspectColumn = aspectColumns[j];
                        if (aspectColumn) {
                            writeAspectColumn(aspectColumn, eid, state[j], false);
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
            let hasAspect = false;
            for (let i = 0; i < aspectColumns.length; i++) {
                if (aspectColumns[i]) {
                    hasAspect = true;
                    break;
                }
            }
            if (!hasAspect) {
                callback(stores as unknown as StoresFromParameters<T>, entities);
                return results;
            }
            const filtered: Store<any>[] = [];
            for (let i = 0; i < stores.length; i++) {
                if (!aspectColumns[i]) filtered.push(stores[i]);
            }
            callback(filtered as unknown as StoresFromParameters<T>, entities);
            return results;
        },

        select<U extends QueryParameter[]>(...params: U): QueryResult<U> {
            traits.length = 0;
            stores.length = 0;
            aspectColumns.length = 0;
            getQueryStores(params, traits, stores, world, aspectColumns);
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
    untrackedIndices: number[],
    aspectColumns: (AspectColumn | undefined)[]
) {
    for (let i = 0; i < traits.length; i++) {
        const column = aspectColumns[i];
        if (column) {
            if (isAspectColumnTracked(column, world, query)) trackedIndices.push(i);
            else untrackedIndices.push(i);
            continue;
        }
        const trait = traits[i];
        const hasTracked = world[$internal].trackedTraits.has(trait);
        const hasChanged = query.hasChangedModifiers && query.changedTraits.has(trait);

        if (hasTracked || hasChanged) trackedIndices.push(i);
        else untrackedIndices.push(i);
    }
}

function isAspectColumnTracked(column: AspectColumn, world: World, query: QueryInstance) {
    const ctx = world[$internal];
    const completeness = column.aspect[$internal].completeness;
    if (ctx.trackedTraits.has(completeness)) return true;
    if (query.hasChangedModifiers && query.changedTraits.has(completeness)) return true;
    const parts = column.parts;
    for (let i = 0; i < parts.length; i++) {
        const trait = parts[i].trait;
        if (ctx.trackedTraits.has(trait)) return true;
        if (query.hasChangedModifiers && query.changedTraits.has(trait)) return true;
    }
    return false;
}

/* @inline */ function createSnapshots(
    entityId: number,
    traits: Trait[],
    stores: Store<any>[],
    state: any[],
    aspectColumns: (AspectColumn | undefined)[]
) {
    for (let i = 0; i < traits.length; i++) {
        const column = aspectColumns[i];
        if (column) {
            state[i] = readAspectColumn(column, entityId);
            continue;
        }
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
    atomicSnapshots: any[],
    aspectColumns: (AspectColumn | undefined)[]
) {
    for (let j = 0; j < traits.length; j++) {
        const column = aspectColumns[j];
        if (column) {
            state[j] = readAspectColumn(column, entityId);
            atomicSnapshots[j] = null;
            continue;
        }
        const trait = traits[j];
        const ctx = trait[$internal];
        const value = ctx.get(entityId, stores[j]);
        state[j] = value;
        atomicSnapshots[j] = ctx.type === 'aos' ? { ...value } : null;
    }
}

type AspectColumn = {
    aspect: Aspect;
    parts: { trait: Trait; keys: string[]; store: Store }[];
};

function readAspectColumn(column: AspectColumn, entityId: number) {
    const merged: Record<string, unknown> = {};
    const parts = column.parts;
    for (let i = 0; i < parts.length; i++) {
        const part = parts[i];
        const ctx = part.trait[$internal];
        const value = ctx.get(entityId, part.store);
        if (ctx.type === 'aos') {
            if (value && typeof value === 'object') {
                const record = value as Record<string, unknown>;
                const keys = part.keys;
                for (let k = 0; k < keys.length; k++) merged[keys[k]] = record[keys[k]];
            }
            continue;
        }
        if (value && typeof value === 'object') Object.assign(merged, value);
    }
    return merged;
}

function writeAspectColumn(
    column: AspectColumn,
    entityId: number,
    merged: unknown,
    detect: boolean
): Trait[] {
    const record = (merged && typeof merged === 'object' ? merged : {}) as Record<string, unknown>;
    const changed: Trait[] = [];
    const parts = column.parts;
    for (let i = 0; i < parts.length; i++) {
        const part = parts[i];
        const ctx = part.trait[$internal];
        if (ctx.type === 'aos') {
            const current = ctx.get(entityId, part.store);
            if (!current || typeof current !== 'object') continue;
            const currentRecord = current as Record<string, unknown>;
            let did = false;
            const keys = part.keys;
            for (let k = 0; k < keys.length; k++) {
                const key = keys[k];
                if (!detect) {
                    currentRecord[key] = record[key];
                } else if (currentRecord[key] !== record[key]) {
                    currentRecord[key] = record[key];
                    did = true;
                }
            }
            if (did) changed.push(part.trait);
            continue;
        }
        if (detect) {
            if (ctx.fastSetWithChangeDetection(entityId, part.store, record))
                changed.push(part.trait);
        } else {
            ctx.fastSet(entityId, part.store, record);
        }
    }
    return changed;
}

/* @inline */ export function getQueryStores<T extends QueryParameter[]>(
    params: T,
    traits: Trait[],
    stores: Store<any>[],
    world: World,
    aspectColumns: (AspectColumn | undefined)[]
) {
    const pushTrait = (trait: Trait) => {
        if (trait[$internal].type === 'tag') return;
        traits.push(trait);
        stores.push(getStore(world, trait));
        aspectColumns.push(undefined);
    };

    const pushAspect = (aspect: Aspect) => {
        ensureAspect(world, aspect);
        const parts: AspectColumn['parts'] = [];
        const definitions = aspect[$internal].parts;
        for (let i = 0; i < definitions.length; i++) {
            const part = definitions[i];
            if (!hasTraitInstance(world[$internal].traitInstances, part.trait)) {
                registerTrait(world, part.trait);
            }
            parts.push({
                trait: part.trait,
                keys: part.keys,
                store: getStore(world, part.trait),
            });
        }
        traits.push(aspect[$internal].completeness);
        stores.push(undefined as unknown as Store);
        aspectColumns.push({ aspect, parts });
    };

    const pushSource = (source: Trait | Aspect) => {
        if (isAspect(source)) pushAspect(source);
        else pushTrait(source);
    };

    for (let i = 0; i < params.length; i++) {
        const param = params[i];

        // Handle relation pairs
        if (isRelationPair(param)) {
            const pairCtx = param[$internal];
            const relation = pairCtx.relation as Relation<Trait>;
            const baseTrait = relation[$internal].trait;
            pushTrait(baseTrait);
            continue;
        }

        if (isAspect(param)) {
            pushAspect(param);
            continue;
        }

        if (isModifier(param)) {
            // Skip not modifier.
            if (param.type === 'not') continue;

            const sources = param.sources ?? param.traits;
            for (let j = 0; j < sources.length; j++) pushSource(sources[j] as Trait | Aspect);
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

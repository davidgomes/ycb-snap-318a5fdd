import { $internal } from '../common';
import { getAspect, isAspect, setAspect } from '../aspect/aspect';
import type { Aspect } from '../aspect/types';
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

type QueryReadSlot =
    | { kind: 'trait'; trait: Trait; store: Store<any> }
    | { kind: 'aspect'; aspect: Aspect };

function buildReadSlots(
    params: QueryParameter[],
    traits: Trait[],
    stores: Store<any>[],
    world: World
): QueryReadSlot[] {
    const slots: QueryReadSlot[] = [];

    for (let i = 0; i < params.length; i++) {
        const param = params[i];

        if (isRelationPair(param)) {
            const pairCtx = param[$internal];
            const relation = pairCtx.relation as Relation<Trait>;
            const baseTrait = relation[$internal].trait;
            if (baseTrait[$internal].type !== 'tag') {
                traits.push(baseTrait);
                stores.push(getStore(world, baseTrait));
                slots.push({ kind: 'trait', trait: baseTrait, store: stores[stores.length - 1] });
            }
            continue;
        }

        if (isModifier(param)) {
            if (param.type === 'not') continue;

            if (param.aspect) {
                slots.push({ kind: 'aspect', aspect: param.aspect });
                const dataTraits = param.aspect[$internal].dataTraits;
                for (let j = 0; j < dataTraits.length; j++) {
                    const trait = dataTraits[j];
                    traits.push(trait);
                    stores.push(getStore(world, trait));
                }
                continue;
            }

            const modifierTraits = param.traits;
            for (let j = 0; j < modifierTraits.length; j++) {
                const trait = modifierTraits[j];
                if (trait[$internal].type === 'tag') continue;
                traits.push(trait);
                stores.push(getStore(world, trait));
                slots.push({ kind: 'trait', trait, store: stores[stores.length - 1] });
            }
            continue;
        }

        if (isAspect(param)) {
            slots.push({ kind: 'aspect', aspect: param });
            const dataTraits = param[$internal].dataTraits;
            for (let j = 0; j < dataTraits.length; j++) {
                const trait = dataTraits[j];
                traits.push(trait);
                stores.push(getStore(world, trait));
            }
            continue;
        }

        const trait = param as Trait;
        if (trait[$internal].type === 'tag') continue;
        traits.push(trait);
        stores.push(getStore(world, trait));
        slots.push({ kind: 'trait', trait, store: stores[stores.length - 1] });
    }

    return slots;
}

function createReadSlotSnapshots(
    world: World,
    entity: Entity,
    eid: number,
    slots: QueryReadSlot[],
    state: any[],
    atomicSnapshots?: any[]
) {
    for (let i = 0; i < slots.length; i++) {
        const slot = slots[i];
        if (slot.kind === 'aspect') {
            const value = getAspect(world, entity, slot.aspect);
            state[i] = value ? { ...value } : {};
            if (atomicSnapshots) atomicSnapshots[i] = { ...state[i] };
        } else {
            const value = slot.trait[$internal].get(eid, slot.store);
            state[i] = value;
            if (atomicSnapshots) {
                atomicSnapshots[i] = slot.trait[$internal].type === 'aos' ? { ...value } : null;
            }
        }
    }
}

function commitReadSlotSnapshots(
    world: World,
    entity: Entity,
    eid: number,
    slots: QueryReadSlot[],
    state: any[],
    atomicSnapshots: any[] | null,
    changeDetection: QueryResultOptions['changeDetection'],
    query: QueryInstance,
    changedPairs: [Entity, Trait][]
) {
    for (let i = 0; i < slots.length; i++) {
        const slot = slots[i];

        if (slot.kind === 'aspect') {
            const triggerChanged = changeDetection !== 'never';
            setAspect(world, entity, slot.aspect, state[i], triggerChanged);
            continue;
        }

        const trait = slot.trait;
        const ctx = trait[$internal];
        const newValue = state[i];
        const store = slot.store;

        if (changeDetection === 'never') {
            ctx.fastSet(eid, store, newValue);
            continue;
        }

        const hasTracked = world[$internal].trackedTraits.has(trait);
        const hasChanged = query.hasChangedModifiers && query.changedTraits.has(trait);
        const shouldDetect =
            changeDetection === 'always' || (changeDetection === 'auto' && (hasTracked || hasChanged));

        if (!shouldDetect) {
            ctx.fastSet(eid, store, newValue);
            continue;
        }

        let changed = false;
        if (ctx.type === 'aos') {
            changed = ctx.fastSetWithChangeDetection(eid, store, newValue);
            if (!changed && atomicSnapshots) {
                changed = !shallowEqual(newValue, atomicSnapshots[i]);
            }
        } else {
            changed = ctx.fastSetWithChangeDetection(eid, store, newValue);
        }

        if (changed) changedPairs.push([entity, trait]);
    }
}

export function createQueryResult<T extends QueryParameter[]>(
    world: World,
    entities: Entity[],
    query: QueryInstance,
    params: QueryParameter[]
): QueryResult<T> {
    const traits: Trait[] = [];
    const stores: Store<any>[] = [];
    const readSlots = buildReadSlots(params, traits, stores, world);

    const results = Object.assign(entities, {
        readEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void
        ) {
            const state = Array.from({ length: readSlots.length }) as InstancesFromParameters<T>;

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);
                createReadSlotSnapshots(world, entity, eid, readSlots, state);
                callback(state, entity, i);
            }

            return results;
        },

        updateEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void,
            options: QueryResultOptions = { changeDetection: 'auto' }
        ) {
            const state = Array.from({ length: readSlots.length });
            const changedPairs: [Entity, Trait][] = [];
            const atomicSnapshots =
                options.changeDetection === 'auto' || options.changeDetection === 'always'
                    ? []
                    : null;

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);

                createReadSlotSnapshots(
                    world,
                    entity,
                    eid,
                    readSlots,
                    state,
                    atomicSnapshots ?? undefined
                );
                callback(state as unknown as InstancesFromParameters<T>, entity, i);

                if (!world.has(entity)) continue;

                commitReadSlotSnapshots(
                    world,
                    entity,
                    eid,
                    readSlots,
                    state,
                    atomicSnapshots,
                    options.changeDetection ?? 'auto',
                    query,
                    changedPairs
                );
            }

            for (let i = 0; i < changedPairs.length; i++) {
                const [entity, trait] = changedPairs[i];
                setChanged(world, entity, trait);
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
            readSlots.length = 0;
            readSlots.push(...buildReadSlots(params, traits, stores, world));
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

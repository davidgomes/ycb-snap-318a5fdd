import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { getAliveEntities } from '../entity/utils/entity-index';
import { setChanged } from '../query/modifiers/changed';
import { isOrderedTrait } from '../relation/ordered';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import type { Schema, StoreType } from '../storage';
import {
    addTrait,
    addTraitToEntity,
    allocateTraitId,
    getStore,
    hasTrait,
    registerTrait,
    removeTrait,
    removeTraitFromEntity,
} from '../trait/trait';
import { getTraitInstance, hasTraitInstance, setTraitInstance } from '../trait/trait-instance';
import type { Trait, TraitInstance } from '../trait/types';
import { universe } from '../universe/universe';
import type { World } from '../world';
import { incrementWorldBitflag } from '../world/utils/increment-world-bit-flag';
import { aspectHooks } from './aspect-hooks';
import { isAspect } from './is-aspect';
import { $aspect } from './symbols';
import type { Aspect, AspectInput, AspectOperations, FlattenAspectInputs } from './types';

/** Bookkeeping handle. A specific aspect schema is not assignable to the default one. */
type AnyAspect = Trait & {
    readonly traits: readonly Trait[];
    readonly [$aspect]: true;
};

type PartKind = 'soa' | 'aos' | 'tag';

type PartDef = {
    trait: Trait;
    keys: string[];
    type: PartKind;
};

type RuntimePart = {
    def: PartDef;
    store: any;
};

type AspectStore = {
    world: World;
    parts: RuntimePart[];
    /** Constituents written for an entity, consumed by commit. Indexed by entity id. */
    pending: (Trait[] | undefined)[];
};

const aspectsByTraitId: AnyAspect[][] = [];
const removalGuard = new Set<string>();
const committing = new Set<AnyAspect>();
const siblingQueue: { world: World; entity: Entity; aspect: AnyAspect }[] = [];
const siblingKeys = new Set<string>();
let flushingSiblings = false;

function guardKey(aspect: AnyAspect, entity: Entity) {
    return `${aspect.id}:${entity}`;
}

function hasAll(world: World, entity: Entity, traits: readonly Trait[]) {
    for (let i = 0; i < traits.length; i++) {
        if (!hasTrait(world, entity, traits[i])) return false;
    }
    return true;
}

function entityFromIndex(world: World, index: number): Entity | undefined {
    const entityIndex = world[$internal].entityIndex;
    const denseIndex = entityIndex.sparse[index];
    if (denseIndex === undefined || denseIndex >= entityIndex.aliveCount) return undefined;
    const entity = entityIndex.dense[denseIndex];
    if (getEntityId(entity) !== index) return undefined;
    return entity;
}

function ensureMaskRow(rows: number[][], generationId: number) {
    if (!rows[generationId]) rows[generationId] = [];
    return rows[generationId];
}

/**
 * Record that an entity already has every constituent.
 * Existing tracking snapshots are patched so the backfill is not a new edge.
 */
function silencePresence(world: World, entity: Entity, aspect: AnyAspect) {
    const ctx = world[$internal];
    const instance = getTraitInstance(ctx.traitInstances, aspect);
    if (!instance) return;

    const { generationId, bitflag } = instance;
    const eid = getEntityId(entity);
    const masks = ctx.entityMasks[generationId];
    if (!masks) return;

    const current = masks[eid] ?? 0;
    if ((current & bitflag) === bitflag) return;
    masks[eid] = current | bitflag;

    for (const snapshot of ctx.trackingSnapshots.values()) {
        const row = ensureMaskRow(snapshot, generationId);
        row[eid] = (row[eid] ?? 0) | bitflag;
    }
}

function liveAdd(world: World, entity: Entity, aspect: AnyAspect) {
    const instance = addTraitToEntity(world, entity, aspect);
    if (!instance) return;

    // Presence is derived from constituents. Destroy iterates real traits only.
    world[$internal].entityTraits.get(entity)?.delete(aspect);

    if (!hasTrait(world, entity, aspect) || !world.has(entity)) return;
    for (const sub of instance.addSubscriptions) sub(entity);
}

function liveRemove(world: World, entity: Entity, aspect: AnyAspect) {
    removeTraitFromEntity(world, entity, aspect);
}

function indexAspect(aspect: AnyAspect, traits: readonly Trait[]) {
    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        let list = aspectsByTraitId[trait.id];
        if (!list) {
            list = [];
            aspectsByTraitId[trait.id] = list;
        }
        list.push(aspect);
    }
}

function prepareMaskSlots(world: World, generationId: number) {
    const ctx = world[$internal];
    for (const dirtyMask of ctx.dirtyMasks.values()) ensureMaskRow(dirtyMask, generationId);
    for (const changedMask of ctx.changedMasks.values()) ensureMaskRow(changedMask, generationId);
    for (const snapshot of ctx.trackingSnapshots.values()) ensureMaskRow(snapshot, generationId);
}

function linkStore(world: World, parts: PartDef[]): AspectStore {
    const runtimeParts: RuntimePart[] = [];
    for (let i = 0; i < parts.length; i++) {
        const def = parts[i];
        runtimeParts.push({
            def,
            store: def.type === 'tag' ? null : getStore(world, def.trait),
        });
    }
    return { world, parts: runtimeParts, pending: [] };
}

function createAspectInstance(world: World, aspect: AnyAspect, parts: PartDef[]) {
    const ctx = world[$internal];
    const data = {
        generationId: ctx.entityMasks.length - 1,
        bitflag: ctx.bitflag,
        trait: aspect,
        store: linkStore(world, parts),
        queries: new Set(),
        trackingQueries: new Set(),
        notQueries: new Set(),
        relationQueries: new Set(),
        schema: aspect.schema,
        changeSubscriptions: new Set(),
        addSubscriptions: new Set(),
        removeSubscriptions: new Set(),
    };

    setTraitInstance(ctx.traitInstances, aspect, data as unknown as TraitInstance);
    world.traits.add(aspect);
    incrementWorldBitflag(world);
    prepareMaskSlots(world, data.generationId);
    return data;
}

function backfill(world: World, aspect: AnyAspect, traits: readonly Trait[], skip?: Entity) {
    const entities = getAliveEntities(world[$internal].entityIndex);
    for (let i = 0; i < entities.length; i++) {
        const entity = entities[i];
        if (skip !== undefined && entity === skip) continue;
        if (!hasAll(world, entity, traits)) continue;
        silencePresence(world, entity, aspect);
    }
}

function ensureRegistered(world: World, aspect: AnyAspect, skip?: Entity) {
    if (!world.isInitialized) return;
    if (hasTraitInstance(world[$internal].traitInstances, aspect)) return;
    (aspect[$internal] as unknown as AspectOperations).register(world, skip);
}

function assignPart(
    part: RuntimePart,
    index: number,
    value: Record<string, unknown>,
    mode: 'partial' | 'all',
    track: 'touch' | 'compare' | 'none'
) {
    const { def, store } = part;
    if (def.type === 'tag' || def.keys.length === 0 || !store) return false;

    let touched = false;
    let changed = false;

    if (def.type === 'soa') {
        for (let k = 0; k < def.keys.length; k++) {
            const key = def.keys[k];
            if (mode === 'partial' && !(key in value)) continue;
            const next = value[key];
            if (track !== 'none' && store[key][index] !== next) changed = true;
            store[key][index] = next;
            touched = true;
        }
    } else {
        const inst = store[index];
        if (!inst) return false;
        for (let k = 0; k < def.keys.length; k++) {
            const key = def.keys[k];
            if (mode === 'partial' && !(key in value)) continue;
            const next = value[key];
            if (track !== 'none' && inst[key] !== next) changed = true;
            inst[key] = next;
            touched = true;
        }
    }

    if (track === 'none') return false;
    if (track === 'compare') return changed;
    return touched;
}

function writeParts(
    store: AspectStore,
    index: number,
    value: Record<string, unknown>,
    mode: 'partial' | 'all',
    track: 'touch' | 'compare' | 'none'
) {
    const entity = entityFromIndex(store.world, index);
    const changed: Trait[] = [];
    // The world entity packs to 0, so only undefined is a missing entity.
    if (entity === undefined) return changed;

    const parts = store.parts;
    for (let i = 0; i < parts.length; i++) {
        const part = parts[i];
        if (!hasTrait(store.world, entity, part.def.trait)) continue;
        if (assignPart(part, index, value, mode, track)) changed.push(part.def.trait);
    }
    return changed;
}

function readParts(store: AspectStore, index: number) {
    const result: Record<string, unknown> = {};
    const parts = store.parts;
    for (let i = 0; i < parts.length; i++) {
        const part = parts[i];
        if (part.def.type === 'tag' || part.def.keys.length === 0 || !part.store) continue;
        if (part.def.type === 'soa') {
            for (let k = 0; k < part.def.keys.length; k++) {
                const key = part.def.keys[k];
                result[key] = part.store[key][index];
            }
        } else {
            const inst = part.store[index];
            if (!inst) continue;
            for (let k = 0; k < part.def.keys.length; k++) {
                const key = part.def.keys[k];
                result[key] = inst[key];
            }
        }
    }
    return result;
}

function isCommitting(aspect: AnyAspect) {
    return committing.has(aspect);
}

function queueSibling(world: World, entity: Entity, aspect: AnyAspect) {
    const key = guardKey(aspect, entity);
    if (siblingKeys.has(key)) return;
    siblingKeys.add(key);
    siblingQueue.push({ world, entity, aspect });
}

function flushSiblings() {
    if (aspectHooks.depth !== 0 || flushingSiblings) return;
    flushingSiblings = true;
    try {
        while (siblingQueue.length > 0) {
            const batch = siblingQueue.splice(0, siblingQueue.length);
            siblingKeys.clear();
            for (let i = 0; i < batch.length; i++) {
                const item = batch[i];
                if (!hasTrait(item.world, item.entity, item.aspect)) continue;
                markAspectOnly(item.world, item.entity, item.aspect);
            }
        }
    } finally {
        flushingSiblings = false;
    }
}

function markAspectOnly(world: World, entity: Entity, aspect: AnyAspect) {
    aspectHooks.depth++;
    try {
        setChanged(world, entity, aspect);
    } finally {
        aspectHooks.depth--;
        if (aspectHooks.depth === 0) flushSiblings();
    }
}

function onAdded(world: World, entity: Entity, trait: Trait) {
    const list = aspectsByTraitId[trait.id];
    if (!list) return;

    for (let i = 0; i < list.length; i++) {
        const aspect = list[i];
        const registered = hasTraitInstance(world[$internal].traitInstances, aspect);
        if (!registered) ensureRegistered(world, aspect, entity);
        if (hasTrait(world, entity, aspect)) continue;
        if (!hasAll(world, entity, aspect.traits)) continue;
        liveAdd(world, entity, aspect);
    }
}

function onRemoving(world: World, entity: Entity, trait: Trait) {
    const list = aspectsByTraitId[trait.id];
    if (!list) return;

    for (let i = 0; i < list.length; i++) {
        const aspect = list[i];
        if (!hasTrait(world, entity, aspect)) continue;
        const key = guardKey(aspect, entity);
        if (removalGuard.has(key)) continue;
        removalGuard.add(key);

        const instance = getTraitInstance(world[$internal].traitInstances, aspect);
        if (!instance) continue;
        for (const sub of instance.removeSubscriptions) sub(entity);
    }
}

function onRemoved(world: World, entity: Entity, trait: Trait) {
    const list = aspectsByTraitId[trait.id];
    if (!list) return;

    for (let i = 0; i < list.length; i++) {
        const aspect = list[i];
        const key = guardKey(aspect, entity);
        if (!hasTrait(world, entity, aspect) || hasAll(world, entity, aspect.traits)) {
            removalGuard.delete(key);
            continue;
        }
        liveRemove(world, entity, aspect);
        removalGuard.delete(key);
    }
}

function onConstituentChanged(world: World, entity: Entity, trait: Trait) {
    const list = aspectsByTraitId[trait.id];
    if (!list) return;

    for (let i = 0; i < list.length; i++) {
        const aspect = list[i];
        if (!hasTrait(world, entity, aspect)) continue;
        if (aspectHooks.depth > 0) {
            if (!isCommitting(aspect)) queueSibling(world, entity, aspect);
            continue;
        }
        markAspectOnly(world, entity, aspect);
    }
}

aspectHooks.added = onAdded;
aspectHooks.removing = onRemoving;
aspectHooks.removed = onRemoved;
aspectHooks.changed = onConstituentChanged;

function traitKeys(trait: Trait): {
    keys: string[];
    type: PartKind;
    schema: Record<string, unknown>;
} {
    const internal = trait[$internal];
    if (internal.type === 'tag') return { keys: [], type: 'tag', schema: {} };

    if (internal.type === 'aos') {
        const sample = (trait.schema as () => unknown)();
        if (sample === null || typeof sample !== 'object')
            return { keys: [], type: 'aos', schema: {} };
        const keys: string[] = [];
        const schema: Record<string, unknown> = {};
        for (const key of Object.keys(sample as Record<string, unknown>)) {
            const value = (sample as Record<string, unknown>)[key];
            if (typeof value === 'function') continue;
            keys.push(key);
            schema[key] = value;
        }
        return { keys, type: 'aos', schema };
    }

    const source = trait.schema as Record<string, unknown>;
    const keys = Object.keys(source);
    const schema: Record<string, unknown> = {};
    for (let i = 0; i < keys.length; i++) schema[keys[i]] = source[keys[i]];
    return { keys, type: 'soa', schema };
}

function flattenInput(input: unknown, into: Trait[]) {
    if (isAspect(input)) {
        const nested = input.traits;
        for (let i = 0; i < nested.length; i++) into.push(nested[i]);
        return;
    }

    if (isRelation(input) || isRelationPair(input)) {
        throw new Error('Koota: Aspects cannot contain relations.');
    }

    if (typeof input !== 'function') {
        throw new Error('Koota: createAspect expected a trait or aspect.');
    }

    const trait = input as Trait;
    if (!trait[$internal] || typeof trait[$internal].id !== 'number') {
        throw new Error('Koota: createAspect expected a trait or aspect.');
    }
    if (trait[$internal].relation || isOrderedTrait(trait)) {
        throw new Error('Koota: Aspects cannot contain relations.');
    }

    into.push(trait);
}

function buildParts(traits: Trait[]) {
    const seen = new Set<number>();
    const fields = new Map<string, Trait>();
    const parts: PartDef[] = [];
    const schema: Record<string, unknown> = {};

    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        if (seen.has(trait.id)) {
            throw new Error('Koota: Aspects cannot contain the same trait more than once.');
        }
        seen.add(trait.id);

        const collected = traitKeys(trait);
        for (let k = 0; k < collected.keys.length; k++) {
            const key = collected.keys[k];
            if (fields.has(key)) {
                throw new Error(
                    `Koota: Aspect field "${key}" is declared by more than one constituent.`
                );
            }
            fields.set(key, trait);
            schema[key] = collected.schema[key];
        }

        parts.push({ trait, keys: collected.keys, type: collected.type });
    }

    const hasData = parts.some((part) => part.keys.length > 0);
    return { parts, schema, type: (hasData ? 'soa' : 'tag') as StoreType };
}

export function createAspect<const T extends readonly [AspectInput, AspectInput, ...AspectInput[]]>(
    ...inputs: T
): Aspect<FlattenAspectInputs<T>> {
    if (inputs.length < 2) throw new Error('Koota: createAspect requires at least two traits.');

    const flat: Trait[] = [];
    for (let i = 0; i < inputs.length; i++) flattenInput(inputs[i], flat);
    if (flat.length < 2) throw new Error('Koota: createAspect requires at least two traits.');

    const { parts, schema, type } = buildParts(flat);
    const traits = Object.freeze(flat.slice()) as unknown as FlattenAspectInputs<T>;
    const id = allocateTraitId();

    function aspect(params?: Record<string, unknown>) {
        return [aspect, params] as [typeof aspect, typeof params];
    }

    const self = aspect as unknown as Aspect<FlattenAspectInputs<T>>;

    const operations: AspectOperations & { register(world: World, skip?: Entity): void } = {
        register(world: World, skip?: Entity) {
            if (!world.isInitialized) return;
            if (hasTraitInstance(world[$internal].traitInstances, self)) return;

            for (let i = 0; i < flat.length; i++) {
                if (!hasTraitInstance(world[$internal].traitInstances, flat[i])) {
                    registerTrait(world, flat[i]);
                }
            }

            createAspectInstance(world, self, parts);
            backfill(world, self, flat, skip);
        },
        addTo(world: World, entity: Entity, params?: Record<string, unknown>) {
            const complete = hasAll(world, entity, flat);
            operations.register(world, complete ? undefined : entity);
            if (hasAll(world, entity, flat)) return;

            for (let i = 0; i < parts.length; i++) {
                const part = parts[i];
                if (hasTrait(world, entity, part.trait)) continue;

                let initial: Record<string, unknown> | undefined;
                if (params && part.keys.length > 0) {
                    const picked: Record<string, unknown> = {};
                    let any = false;
                    for (let k = 0; k < part.keys.length; k++) {
                        const key = part.keys[k];
                        if (key in params) {
                            picked[key] = params[key];
                            any = true;
                        }
                    }
                    if (any) initial = picked;
                }

                if (initial) addTrait(world, entity, [part.trait, initial]);
                else addTrait(world, entity, part.trait);
            }
        },
        removeFrom(world: World, entity: Entity) {
            for (let i = 0; i < flat.length; i++) {
                if (hasTrait(world, entity, flat[i])) removeTrait(world, entity, flat[i]);
            }
        },
        commit(world: World, entity: Entity) {
            const instance = getTraitInstance(world[$internal].traitInstances, self);
            const store = instance?.store as AspectStore | undefined;
            const pending = store ? store.pending[getEntityId(entity)] : undefined;
            if (store) store.pending[getEntityId(entity)] = undefined;

            committing.add(self);
            aspectHooks.depth++;
            try {
                if (pending) {
                    for (let i = 0; i < pending.length; i++) setChanged(world, entity, pending[i]);
                }
                setChanged(world, entity, self);
            } finally {
                aspectHooks.depth--;
                committing.delete(self);
                if (aspectHooks.depth === 0) flushSiblings();
            }
        },
    };

    const internal = {
        id,
        set(index: number, store: AspectStore, value: Record<string, unknown>) {
            const changed = writeParts(store, index, value, 'partial', 'touch');
            store.pending[index] = changed.length > 0 ? changed : undefined;
        },
        fastSet(index: number, store: AspectStore, value: Record<string, unknown>) {
            writeParts(store, index, value, 'all', 'none');
            store.pending[index] = undefined;
            return false;
        },
        fastSetWithChangeDetection(
            index: number,
            store: AspectStore,
            value: Record<string, unknown>
        ) {
            const changed = writeParts(store, index, value, 'all', 'compare');
            store.pending[index] = changed.length > 0 ? changed : undefined;
            return changed.length > 0;
        },
        get(index: number, store: AspectStore) {
            return readParts(store, index);
        },
        createStore: () => ({ world: null, parts: [], pending: [] }) as unknown as Schema,
        relation: null,
        type,
        ...operations,
    };

    Object.assign(aspect, {
        [$aspect]: true as const,
        [$internal]: internal,
    });

    Object.defineProperty(aspect, 'id', {
        value: id,
        writable: false,
        enumerable: true,
        configurable: false,
    });
    Object.defineProperty(aspect, 'schema', {
        value: schema,
        writable: false,
        enumerable: true,
        configurable: false,
    });
    Object.defineProperty(aspect, 'traits', {
        value: traits,
        writable: false,
        enumerable: true,
        configurable: false,
    });

    indexAspect(self, flat);

    for (const world of universe.worlds) {
        if (!world?.isInitialized) continue;
        operations.register(world);
    }

    return self;
}

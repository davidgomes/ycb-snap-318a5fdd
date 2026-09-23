import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { destroyEntity } from '../entity/entity';
import { allocateEntity, isEntityAlive, releaseEntity } from '../entity/utils/entity-index';
import { getEntityId } from '../entity/utils/pack-entity';
import { setChanged, setPairChanged } from '../query/modifiers/changed';
import { commitQueryRemovals } from '../query/query';
import type { QueryInstance } from '../query/types';
import { getRelationData, getRelationTargets, hasRelationToTarget } from '../relation/relation';
import type { Relation, RelationPair } from '../relation/types';
import { isRelationPair } from '../relation/utils/is-relation';
import { getSchemaDefaults } from '../storage';
import { getTraitInstance } from '../trait/trait-instance';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import type { ConfigurableTrait, Trait } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world';
import {
    createDeferredBuffer,
    type DeferredBuffer,
    type DeferredCommand,
    type DeferredRead,
} from './types';

type ValueSlot = { value: unknown };
type TraitMap = Map<Trait, ValueSlot>;
type RelationMap = Map<Relation, Map<Entity, ValueSlot>>;

type VEntity = {
    alive: boolean;
    traits: TraitMap;
    relations: RelationMap;
};

type Snap = {
    alive: boolean;
    traits: TraitMap;
    relations: RelationMap;
    queries: Set<QueryInstance>;
};

type OverlayCache = { dirty: boolean; map: Map<Entity, VEntity> };

const overlays = new WeakMap<World, OverlayCache>();

/**
 * Install `world.deferred`.
 * `updateEach` pushes a buffer that flushes on exit and leaves outer buffers queued.
 */
export function installDeferred(world: World) {
    const ctx = world[$internal];
    if (ctx.deferredStack.length === 0) ctx.deferredStack.push(createDeferredBuffer());

    world.deferred = {
        spawn(...traits: ConfigurableTrait[]) {
            return deferredSpawn(world, traits);
        },
        destroy(entity: Entity) {
            enqueue(world, { kind: 'destroy', entity });
        },
        add(entity: Entity, ...traits: ConfigurableTrait[]) {
            enqueue(world, { kind: 'add', entity, traits: traits.slice() });
        },
        remove(entity: Entity, ...traits: (Trait | RelationPair)[]) {
            enqueue(world, { kind: 'remove', entity, traits: traits.slice() });
        },
        addExclusive(entity: Entity, pair: RelationPair) {
            enqueue(world, { kind: 'addExclusive', entity, pair });
        },
        flush() {
            flushBuffer(world, topBuffer(world));
        },
    };

    ctx.beforeMutation = (entity) => flushForMutation(world, entity);
    ctx.enterDeferred = () => {
        ctx.deferredStack.push(createDeferredBuffer());
    };
    ctx.exitDeferred = () => {
        const stack = ctx.deferredStack;
        if (stack.length <= 1) return;
        const buffer = stack.pop()!;
        flushBuffer(world, buffer);
    };
    ctx.readHas = (entity, trait) => readHas(world, entity, trait);
    ctx.readGet = (entity, trait) => readGet(world, entity, trait);
    ctx.readTargets = (entity, relation) => readTargets(world, entity, relation);
    ctx.readAlive = (entity) => readAlive(world, entity);
}

/** Drop queued work. Reserved spawns are released without ever materializing. */
export function resetDeferred(world: World) {
    const ctx = world[$internal];
    const reserved = [...ctx.reservedEntities];
    ctx.reservedEntities.clear();
    ctx.deferredStack = [createDeferredBuffer()];
    ctx.deferredViewDirty = true;
    for (const entity of reserved) releaseReserved(world, entity);
    markDirty(world);
}

function topBuffer(world: World): DeferredBuffer {
    const stack = world[$internal].deferredStack;
    return stack[stack.length - 1]!;
}

function hasPending(world: World): boolean {
    const stack = world[$internal].deferredStack;
    for (let i = 0; i < stack.length; i++) {
        if (stack[i]!.commands.length > 0) return true;
    }
    return false;
}

function enqueue(world: World, command: DeferredCommand) {
    const buffer = topBuffer(world);
    if (buffer.nullified.has(command.entity)) return;

    // Spawn + destroy never materializes, including when the spawn is still queued
    // in an outer buffer. Otherwise the inner destroy would flush first and miss it.
    if (command.kind === 'destroy' && cancelSpawn(world, command.entity)) return;

    buffer.commands.push(command);
    buffer.entities.add(command.entity);
    if (command.kind === 'spawn') buffer.spawned.add(command.entity);
    markDirty(world);
}

function cancelSpawn(world: World, entity: Entity): boolean {
    const stack = world[$internal].deferredStack;
    let found = false;
    for (let i = 0; i < stack.length; i++) {
        if (stack[i]!.spawned.has(entity)) {
            found = true;
            break;
        }
    }
    if (!found) return false;

    for (let i = 0; i < stack.length; i++) {
        const candidate = stack[i]!;
        if (candidate.spawned.has(entity) || candidate.entities.has(entity)) {
            nullify(world, candidate, entity);
        }
    }
    // Later commands in this scope must not recreate the cancelled spawn.
    topBuffer(world).nullified.add(entity);
    markDirty(world);
    return true;
}

function nullify(world: World, buffer: DeferredBuffer, entity: Entity) {
    buffer.nullified.add(entity);
    buffer.spawned.delete(entity);
    buffer.commands = buffer.commands.filter((command) => command.entity !== entity);
    buffer.entities.clear();
    buffer.spawned.clear();
    for (const command of buffer.commands) {
        buffer.entities.add(command.entity);
        if (command.kind === 'spawn') buffer.spawned.add(command.entity);
    }
    if (world[$internal].reservedEntities.has(entity)) releaseReserved(world, entity);
    markDirty(world);
}

function deferredSpawn(world: World, traits: ConfigurableTrait[]): Entity {
    const ctx = world[$internal];
    const entity = allocateEntity(ctx.entityIndex);
    ctx.entityTraits.set(entity, new Set());
    ctx.reservedEntities.add(entity);
    enqueue(world, { kind: 'spawn', entity, traits: traits.slice() });
    return entity;
}

function releaseReserved(world: World, entity: Entity) {
    const ctx = world[$internal];
    ctx.reservedEntities.delete(entity);
    detachFromQueries(world, entity);
    ctx.entityTraits.delete(entity);
    if (!isEntityAlive(ctx.entityIndex, entity)) return;

    const eid = getEntityId(entity);
    for (let i = 0; i < ctx.entityMasks.length; i++) {
        const masks = ctx.entityMasks[i];
        if (masks) masks[eid] = 0;
    }
    releaseEntity(ctx.entityIndex, entity);
}

function detachFromQueries(world: World, entity: Entity) {
    const ctx = world[$internal];
    const visit = (query: QueryInstance | undefined) => {
        if (!query) return;
        query.entities.remove(entity);
        query.toRemove.remove(entity);
    };
    for (const query of ctx.queriesHashMap.values()) visit(query);
    for (let i = 0; i < ctx.queryInstances.length; i++) visit(ctx.queryInstances[i]);
}

/**
 * An immediate mutation runs after deferred commands that target this entity.
 * Inner buffers flush first. Buffers outside the outermost one that mentions
 * the entity stay queued.
 */
function flushForMutation(world: World, entity: Entity) {
    const ctx = world[$internal];
    if (ctx.applyingDeferred) return;

    const stack = ctx.deferredStack;
    let outermost = -1;
    for (let i = 0; i < stack.length; i++) {
        if (stack[i]!.entities.has(entity)) {
            outermost = i;
            break;
        }
    }
    if (outermost < 0) return;

    for (let i = stack.length - 1; i >= outermost; i--) {
        flushBuffer(world, stack[i]!);
    }
}

function flushBuffer(world: World, buffer: DeferredBuffer) {
    if (buffer.commands.length === 0) {
        buffer.spawned.clear();
        buffer.nullified.clear();
        buffer.entities.clear();
        return;
    }

    const commands = buffer.commands;
    buffer.commands = [];
    buffer.entities.clear();
    buffer.spawned.clear();
    buffer.nullified.clear();

    const ctx = world[$internal];
    const snapshots = new Map<Entity, Snap>();
    const previousCapture = ctx.captureSnapshot;
    const previousSuppress = ctx.suppressSubscriptions;
    const previousApplying = ctx.applyingDeferred;

    ctx.suppressSubscriptions = true;
    ctx.applyingDeferred = true;
    ctx.deferredViewDirty = true;
    ctx.captureSnapshot = (entity) => {
        if (!snapshots.has(entity)) snapshots.set(entity, capture(world, entity));
    };

    try {
        for (let i = 0; i < commands.length; i++) {
            try {
                executeCommand(world, commands[i]!);
            } catch (error) {
                for (let j = i; j < commands.length; j++) {
                    const pending = commands[j]!;
                    if (pending.kind === 'spawn' && ctx.reservedEntities.has(pending.entity)) {
                        releaseReserved(world, pending.entity);
                    }
                }
                throw error;
            }
        }
    } finally {
        commitQueryRemovals(world);
        ctx.suppressSubscriptions = previousSuppress;
        ctx.applyingDeferred = previousApplying;
        ctx.captureSnapshot = previousCapture;
        ctx.deferredViewDirty = true;
        markDirty(world);
        if (!previousSuppress) emitDiffs(world, snapshots);
    }
}

function executeCommand(world: World, command: DeferredCommand) {
    switch (command.kind) {
        case 'spawn':
            executeSpawn(world, command.entity, command.traits);
            return;
        case 'destroy':
            executeDestroy(world, command.entity);
            return;
        case 'add':
            if (!isCreated(world, command.entity)) return;
            for (let i = 0; i < command.traits.length; i++) {
                applyAdd(world, command.entity, command.traits[i]!);
            }
            return;
        case 'remove':
            if (!isCreated(world, command.entity)) return;
            if (command.traits.length > 0) removeTrait(world, command.entity, ...command.traits);
            return;
        case 'addExclusive':
            if (!isCreated(world, command.entity)) return;
            applyAddExclusive(world, command.entity, command.pair);
            return;
    }
}

function executeSpawn(world: World, entity: Entity, traits: ConfigurableTrait[]) {
    const ctx = world[$internal];
    if (!ctx.reservedEntities.has(entity)) return;
    if (!isEntityAlive(ctx.entityIndex, entity)) return;
    materialize(world, entity, traits);
}

function executeDestroy(world: World, entity: Entity) {
    if (entity === world[$internal].worldEntity) {
        throw new Error('Koota: Cannot destroy the world entity.');
    }
    // Reserved by an outer buffer, or already dead: skip.
    if (!isCreated(world, entity)) return;
    destroyEntity(world, entity);
}

function materialize(world: World, entity: Entity, traits: ConfigurableTrait[]) {
    const ctx = world[$internal];
    // Snapshot while the entity is still reserved so it counts as not alive yet.
    ctx.captureSnapshot?.(entity);
    ctx.reservedEntities.delete(entity);
    if (!ctx.entityTraits.has(entity)) ctx.entityTraits.set(entity, new Set());

    for (const query of ctx.notQueries) {
        const match = query.check(world, entity);
        if (match) query.add(entity);
        query.resetTrackingBitmasks(getEntityId(entity));
    }

    for (let i = 0; i < traits.length; i++) applyAdd(world, entity, traits[i]!);
}

function applyAdd(world: World, entity: Entity, config: ConfigurableTrait) {
    if (!isCreated(world, entity)) return;

    if (isRelationPair(config)) {
        const pair = config;
        const target = pair[$internal].target;
        if (typeof target !== 'number') return;
        const relation = pair[$internal].relation;
        const params = pair[$internal].params;
        if (hasRelationToTarget(world, relation, entity, target)) {
            if (params) setTrait(world, entity, pair, relationValue(relation, params), false);
            return;
        }
        addTrait(world, entity, pair);
        return;
    }

    const { trait, params, hasParams } = splitTrait(config);
    if (trait[$internal].relation) return;

    if (!hasTrait(world, entity, trait)) {
        addTrait(world, entity, hasParams ? [trait, params] : trait);
        return;
    }
    if (hasParams) setTrait(world, entity, trait, traitValue(trait, params, true), false);
}

function applyAddExclusive(world: World, entity: Entity, pair: RelationPair) {
    if (!isCreated(world, entity)) return;

    const { relation, target, params } = pair[$internal];
    const relationTrait = relation[$internal].trait;

    if (target === '*') {
        if (hasTrait(world, entity, relationTrait)) removeTrait(world, entity, pair);
        return;
    }
    if (typeof target !== 'number') return;

    const current = getRelationTargets(world, relation, entity);
    for (let i = 0; i < current.length; i++) {
        const existing = current[i]!;
        if (existing !== target) removeTrait(world, entity, relation(existing));
    }

    if (hasRelationToTarget(world, relation, entity, target)) {
        if (params) setTrait(world, entity, pair, relationValue(relation, params), false);
        return;
    }
    addTrait(world, entity, pair);
}

function splitTrait(config: ConfigurableTrait): {
    trait: Trait;
    params: unknown;
    hasParams: boolean;
} {
    if (Array.isArray(config)) {
        return { trait: config[0], params: config[1], hasParams: true };
    }
    return { trait: config as Trait, params: undefined, hasParams: false };
}

function isCreated(world: World, entity: Entity): boolean {
    const ctx = world[$internal];
    return isEntityAlive(ctx.entityIndex, entity) && !ctx.reservedEntities.has(entity);
}

// --- Reads: pending buffers applied inner-first, matching scope unwind. ---

function readHas(world: World, entity: Entity, trait: Trait | RelationPair): boolean | null {
    const state = overlayEntity(world, entity);
    if (!state) return null;
    if (!state.alive) return false;

    if (isRelationPair(trait)) {
        const { relation, target } = trait[$internal];
        const targets = state.relations.get(relation);
        if (!targets || targets.size === 0) return false;
        if (target === '*') return true;
        return targets.has(target as Entity);
    }

    const relation = trait[$internal].relation;
    if (relation) {
        const targets = state.relations.get(relation);
        return !!targets && targets.size > 0;
    }
    return state.traits.has(trait);
}

function readGet(world: World, entity: Entity, trait: Trait | RelationPair): DeferredRead | null {
    const state = overlayEntity(world, entity);
    if (!state) return null;
    if (!state.alive) return { hit: true, value: undefined };

    if (isRelationPair(trait)) {
        const { relation, target } = trait[$internal];
        if (typeof target !== 'number') return { hit: true, value: undefined };
        const slot = state.relations.get(relation)?.get(target);
        if (!slot) return { hit: true, value: undefined };
        return { hit: true, value: present(relation[$internal].trait, slot.value) };
    }

    const relation = trait[$internal].relation;
    if (relation) return { hit: true, value: undefined };

    const slot = state.traits.get(trait);
    if (!slot) return { hit: true, value: undefined };
    return { hit: true, value: present(trait, slot.value) };
}

function readAlive(world: World, entity: Entity): boolean | null {
    if (!hasPending(world)) return null;
    const state = overlay(world).get(entity);
    if (!state) return null;
    return state.alive;
}

function readTargets(world: World, entity: Entity, relation: Relation): readonly Entity[] | null {
    const state = overlayEntity(world, entity);
    if (!state) return null;
    if (!state.alive) return [];
    const targets = state.relations.get(relation);
    if (!targets) return [];
    return [...targets.keys()];
}

function overlayEntity(world: World, entity: Entity): VEntity | null {
    if (!hasPending(world)) return null;
    return overlay(world).get(entity) ?? null;
}

function markDirty(world: World) {
    const cache = overlays.get(world);
    if (cache) cache.dirty = true;
    world[$internal].deferredViewDirty = true;
}

function overlay(world: World): Map<Entity, VEntity> {
    const ctx = world[$internal];
    let cache = overlays.get(world);
    if (!cache) {
        cache = { dirty: true, map: new Map() };
        overlays.set(world, cache);
    }
    if (cache.dirty || ctx.deferredViewDirty) {
        cache.map = simulate(world);
        cache.dirty = false;
        ctx.deferredViewDirty = false;
    }
    return cache.map;
}

function simulate(world: World): Map<Entity, VEntity> {
    const map = new Map<Entity, VEntity>();
    const stack = world[$internal].deferredStack;
    // Innermost buffer flushes first, then the buffers outside it.
    for (let i = stack.length - 1; i >= 0; i--) {
        const commands = stack[i]!.commands;
        for (let j = 0; j < commands.length; j++) {
            if (!applyVirtual(world, map, commands[j]!)) break;
        }
    }
    return map;
}

/** Returns false when the rest of this buffer will not run (world-entity destroy). */
function applyVirtual(world: World, map: Map<Entity, VEntity>, command: DeferredCommand): boolean {
    switch (command.kind) {
        case 'spawn':
            virtualSpawn(map, command.entity, command.traits);
            return true;
        case 'destroy':
            if (command.entity === world[$internal].worldEntity) return false;
            virtualDestroy(world, map, command.entity);
            return true;
        case 'add': {
            const state = ensureV(world, map, command.entity);
            if (!state.alive) return true;
            for (let i = 0; i < command.traits.length; i++) virtualAdd(state, command.traits[i]!);
            return true;
        }
        case 'remove': {
            const state = ensureV(world, map, command.entity);
            if (!state.alive) return true;
            for (let i = 0; i < command.traits.length; i++) virtualRemove(state, command.traits[i]!);
            return true;
        }
        case 'addExclusive': {
            const state = ensureV(world, map, command.entity);
            if (!state.alive) return true;
            virtualAddExclusive(state, command.pair);
            return true;
        }
    }
}

function emptyV(): VEntity {
    return { alive: false, traits: new Map(), relations: new Map() };
}

function ensureV(world: World, map: Map<Entity, VEntity>, entity: Entity): VEntity {
    const existing = map.get(entity);
    if (existing) return existing;
    const created = isCreated(world, entity) ? copyReal(world, entity) : emptyV();
    map.set(entity, created);
    return created;
}

function copyReal(world: World, entity: Entity): VEntity {
    const state = emptyV();
    state.alive = true;
    const traits = world[$internal].entityTraits.get(entity);
    if (!traits) return state;

    for (const trait of traits) {
        const relation = trait[$internal].relation;
        if (relation) {
            const targets = getRelationTargets(world, relation, entity);
            if (targets.length === 0) continue;
            const slots = new Map<Entity, ValueSlot>();
            for (let i = 0; i < targets.length; i++) {
                const target = targets[i]!;
                slots.set(target, {
                    value: copyVal(getRelationData(world, entity, relation, target)),
                });
            }
            state.relations.set(relation, slots);
            continue;
        }
        state.traits.set(trait, { value: copyVal(getTrait(world, entity, trait)) });
    }
    return state;
}

function virtualSpawn(map: Map<Entity, VEntity>, entity: Entity, traits: ConfigurableTrait[]) {
    const state = emptyV();
    state.alive = true;
    map.set(entity, state);
    for (let i = 0; i < traits.length; i++) virtualAdd(state, traits[i]!);
}

function virtualAdd(state: VEntity, config: ConfigurableTrait) {
    if (!state.alive) return;
    if (isRelationPair(config)) {
        virtualAddPair(state, config, false);
        return;
    }
    const { trait, params, hasParams } = splitTrait(config);
    if (trait[$internal].relation) return;

    const existing = state.traits.get(trait);
    if (!existing) {
        state.traits.set(trait, { value: traitValue(trait, params, hasParams) });
        return;
    }
    if (!hasParams) return;
    existing.value = traitValue(trait, params, true);
}

function virtualAddPair(state: VEntity, pair: RelationPair, exclusiveOp: boolean) {
    const { relation, target, params } = pair[$internal];
    if (target === '*') {
        if (exclusiveOp) state.relations.delete(relation);
        return;
    }
    if (typeof target !== 'number') return;

    const exclusive = exclusiveOp || relation[$internal].exclusive;
    let targets = state.relations.get(relation);
    const existing = targets?.get(target);

    if (exclusive) {
        targets = new Map();
        state.relations.set(relation, targets);
        const value =
            existing && params
                ? relationValue(relation, params)
                : existing && !params
                  ? existing.value
                  : relationValue(relation, params);
        targets.set(target, { value });
        return;
    }

    if (!targets) {
        targets = new Map();
        state.relations.set(relation, targets);
    }
    if (existing) {
        if (params) existing.value = relationValue(relation, params);
        return;
    }
    targets.set(target, { value: relationValue(relation, params) });
}

function virtualRemove(state: VEntity, config: Trait | RelationPair) {
    if (!state.alive) return;
    if (isRelationPair(config)) {
        const { relation, target } = config[$internal];
        if (target === '*') {
            state.relations.delete(relation);
            return;
        }
        if (typeof target !== 'number') return;
        const targets = state.relations.get(relation);
        if (!targets) return;
        targets.delete(target);
        if (targets.size === 0) state.relations.delete(relation);
        return;
    }

    const relation = config[$internal].relation;
    if (relation) {
        state.relations.delete(relation);
        return;
    }
    state.traits.delete(config);
}

function virtualAddExclusive(state: VEntity, pair: RelationPair) {
    virtualAddPair(state, pair, true);
}

function virtualDestroy(world: World, map: Map<Entity, VEntity>, root: Entity) {
    const queue: Entity[] = [root];
    const processed = new Set<Entity>();

    while (queue.length > 0) {
        const current = queue.pop()!;
        if (processed.has(current)) continue;
        processed.add(current);

        const currentV = ensureV(world, map, current);
        if (!currentV.alive) continue;

        const relations = relationsInPlay(world, map);
        for (let i = 0; i < relations.length; i++) {
            const relation = relations[i]!;
            const auto = relation[$internal].autoDestroy;
            const sources = virtualSources(world, map, relation, current);
            for (let s = 0; s < sources.length; s++) {
                const source = sources[s]!;
                const sourceV = ensureV(world, map, source);
                if (!sourceV.alive) continue;
                const targets = sourceV.relations.get(relation);
                if (targets) {
                    targets.delete(current);
                    if (targets.size === 0) sourceV.relations.delete(relation);
                }
                if (auto === 'source') queue.push(source);
            }

            if (auto === 'target') {
                const targets = currentV.relations.get(relation);
                if (!targets) continue;
                for (const target of targets.keys()) queue.push(target);
            }
        }

        currentV.alive = false;
        currentV.traits.clear();
        currentV.relations.clear();
    }
}

function relationsInPlay(world: World, map: Map<Entity, VEntity>): Relation[] {
    const set = new Set<Relation>(world[$internal].relations);
    for (const state of map.values()) {
        for (const relation of state.relations.keys()) set.add(relation);
    }
    return [...set];
}

function virtualSources(
    world: World,
    map: Map<Entity, VEntity>,
    relation: Relation,
    target: Entity
): Entity[] {
    const result: Entity[] = [];
    const seen = new Set<Entity>();

    const consider = (entity: Entity) => {
        if (seen.has(entity)) return;
        seen.add(entity);
        const state = map.get(entity);
        if (state) {
            if (state.alive && state.relations.get(relation)?.has(target)) result.push(entity);
            return;
        }
        if (!isCreated(world, entity)) return;
        if (hasRelationToTarget(world, relation, entity, target)) result.push(entity);
    };

    const alive = world[$internal].entityIndex.dense;
    const count = world[$internal].entityIndex.aliveCount;
    for (let i = 0; i < count; i++) consider(alive[i]!);
    for (const entity of map.keys()) consider(entity);
    return result;
}

function traitValue(trait: Trait, params: unknown, hasParams: boolean): unknown {
    const type = trait[$internal].type;
    if (type === 'tag') return undefined;
    const defaults = getSchemaDefaults(trait.schema, type);
    if (type === 'aos') return hasParams ? params : defaults;
    if (defaults) return { ...defaults, ...(hasParams ? (params as object) : {}) };
    if (hasParams) return copyVal(params);
    return undefined;
}

function relationValue(relation: Relation, params: unknown): unknown {
    const trait = relation[$internal].trait;
    const type = trait[$internal].type;
    const defaults = getSchemaDefaults(trait.schema, type);
    if (type === 'tag') return {};
    if (type === 'aos') return params ?? defaults ?? {};
    if (defaults) return { ...defaults, ...(params as object) };
    if (params && typeof params === 'object') return { ...(params as object) };
    return {};
}

function copyVal(value: unknown): unknown {
    if (value == null || typeof value !== 'object') return value;
    if (Array.isArray(value)) return value.slice();
    return { ...(value as Record<string, unknown>) };
}

function present(trait: Trait, value: unknown): unknown {
    if (trait[$internal].type === 'aos' || trait[$internal].type === 'tag') return value;
    return copyVal(value);
}

// --- Subscriptions: one event per trait or relation pair whose net state changed. ---

function capture(world: World, entity: Entity): Snap {
    const alive = isCreated(world, entity);
    if (!alive) {
        return { alive: false, traits: new Map(), relations: new Map(), queries: queriesContaining(world, entity) };
    }
    const state = copyReal(world, entity);
    return {
        alive: true,
        traits: state.traits,
        relations: state.relations,
        queries: queriesContaining(world, entity),
    };
}

function queriesContaining(world: World, entity: Entity): Set<QueryInstance> {
    const result = new Set<QueryInstance>();
    const seen = new Set<QueryInstance>();
    const consider = (query: QueryInstance | undefined) => {
        if (!query || seen.has(query)) return;
        seen.add(query);
        if (query.entities.has(entity) && !query.toRemove.has(entity)) result.add(query);
    };

    const ctx = world[$internal];
    for (const query of ctx.queriesHashMap.values()) consider(query);
    for (let i = 0; i < ctx.queryInstances.length; i++) consider(ctx.queryInstances[i]);
    return result;
}

function emitDiffs(world: World, snapshots: Map<Entity, Snap>) {
    for (const [entity, before] of snapshots) {
        emitEntity(world, entity, before);
    }
}

function emitEntity(world: World, entity: Entity, before: Snap) {
    const alive = isCreated(world, entity);
    const afterQueries = alive ? queriesContaining(world, entity) : new Set<QueryInstance>();

    if (!before.alive && !alive) {
        emitQueryDiff(entity, before.queries, afterQueries);
        return;
    }

    if (!alive) {
        emitTraitRemovals(world, entity, before.traits);
        emitPairRemovals(world, entity, before.relations);
        emitQueryDiff(entity, before.queries, afterQueries);
        return;
    }

    const after = copyReal(world, entity);
    if (!before.alive) {
        emitTraitAdditions(world, entity, after.traits);
        emitPairAdditions(world, entity, after.relations);
        emitQueryDiff(entity, before.queries, afterQueries);
        return;
    }

    const traitChanges: Trait[] = [];
    const pairChanges: [Relation, Entity][] = [];

    for (const [trait, prev] of before.traits) {
        const next = after.traits.get(trait);
        if (!next) emitRemoveTrait(world, entity, trait);
        else if (!sameValue(prev.value, next.value)) traitChanges.push(trait);
    }
    for (const [trait] of after.traits) {
        if (!before.traits.has(trait)) emitAddTrait(world, entity, trait);
    }

    for (const [relation, prevTargets] of before.relations) {
        const nextTargets = after.relations.get(relation);
        for (const [target, prev] of prevTargets) {
            const next = nextTargets?.get(target);
            if (!next) emitRemovePair(world, entity, relation, target);
            else if (!sameValue(prev.value, next.value)) pairChanges.push([relation, target]);
        }
    }
    for (const [relation, nextTargets] of after.relations) {
        const prevTargets = before.relations.get(relation);
        for (const target of nextTargets.keys()) {
            if (!prevTargets?.has(target)) emitAddPair(world, entity, relation, target);
        }
    }

    emitQueryDiff(entity, before.queries, afterQueries);

    for (let i = 0; i < traitChanges.length; i++) emitChangeTrait(world, entity, traitChanges[i]!);
    for (let i = 0; i < pairChanges.length; i++) {
        const [relation, target] = pairChanges[i]!;
        emitChangePair(world, entity, relation, target);
    }
}

function emitTraitRemovals(world: World, entity: Entity, traits: TraitMap) {
    for (const trait of traits.keys()) emitRemoveTrait(world, entity, trait);
}

function emitTraitAdditions(world: World, entity: Entity, traits: TraitMap) {
    for (const trait of traits.keys()) emitAddTrait(world, entity, trait);
}

function emitPairRemovals(world: World, entity: Entity, relations: RelationMap) {
    for (const [relation, targets] of relations) {
        for (const target of targets.keys()) emitRemovePair(world, entity, relation, target);
    }
}

function emitPairAdditions(world: World, entity: Entity, relations: RelationMap) {
    for (const [relation, targets] of relations) {
        for (const target of targets.keys()) emitAddPair(world, entity, relation, target);
    }
}

function emitAddTrait(world: World, entity: Entity, trait: Trait) {
    const instance = getTraitInstance(world[$internal].traitInstances, trait);
    if (!instance) return;
    for (const sub of instance.addSubscriptions) sub(entity);
}

function emitRemoveTrait(world: World, entity: Entity, trait: Trait) {
    const instance = getTraitInstance(world[$internal].traitInstances, trait);
    if (!instance) return;
    for (const sub of instance.removeSubscriptions) sub(entity);
}

function emitChangeTrait(world: World, entity: Entity, trait: Trait) {
    setChanged(world, entity, trait);
}

function emitAddPair(world: World, entity: Entity, relation: Relation, target: Entity) {
    const instance = getTraitInstance(world[$internal].traitInstances, relation[$internal].trait);
    if (!instance) return;
    for (const sub of instance.addSubscriptions) sub(entity, target);
}

function emitRemovePair(world: World, entity: Entity, relation: Relation, target: Entity) {
    const instance = getTraitInstance(world[$internal].traitInstances, relation[$internal].trait);
    if (!instance) return;
    for (const sub of instance.removeSubscriptions) sub(entity, target);
}

function emitChangePair(world: World, entity: Entity, relation: Relation, target: Entity) {
    setPairChanged(world, entity, relation[$internal].trait, target);
}

function emitQueryDiff(entity: Entity, before: Set<QueryInstance>, after: Set<QueryInstance>) {
    for (const query of before) {
        if (after.has(query)) continue;
        for (const sub of query.removeSubscriptions) sub(entity);
    }
    for (const query of after) {
        if (before.has(query)) continue;
        for (const sub of query.addSubscriptions) sub(entity);
    }
}

function sameValue(prev: unknown, next: unknown): boolean {
    return shallowEqual(prev, next);
}

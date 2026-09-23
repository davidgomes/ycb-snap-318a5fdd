import { $internal } from '../common';
import { destroyEntity, initEntity } from '../entity/entity';
import type { Entity } from '../entity/types';
import {
    activateReservedEntity,
    isEntityAlive,
    releaseReservedEntity,
    reserveEntity,
} from '../entity/utils/entity-index';
import { getOrderedTraitRelation, isOrderedTrait } from '../relation/ordered';
import { OrderedList } from '../relation/ordered-list';
import {
    getEntitiesWithRelationTo,
    getRelationData,
    getRelationTargets,
    hasRelationPair,
    hasRelationToTarget,
    setRelationData,
} from '../relation/relation';
import type { Relation, RelationPair } from '../relation/types';
import { isRelationPair } from '../relation/utils/is-relation';
import { getSchemaDefaults, type StoreType } from '../storage';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import type { ConfigurableTrait, Trait } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world';
import type {
    Deferred,
    DeferredCommand,
    DeferredInternal,
    DeferredOp,
    DeferredRelationSlot,
    DeferredSimulation,
    DeferredTraitSlot,
    DeferredView,
} from './types';

const worldEntityError =
    'Koota: The world entity cannot be destroyed with deferred commands. Use world.destroy() instead.';

// Pairs read from the world carry no value and are never mutated, so they can share one object.
const unbufferedPair = Object.freeze({ buffered: false, value: undefined });

export function createDeferredInternal(): DeferredInternal {
    return {
        buffers: [[]],
        size: 0,
        destroys: 0,
        subjects: new Map(),
        reserved: new Set(),
        view: null,
    };
}

export function createDeferred(world: World): Deferred {
    return {
        spawn(...traits: ConfigurableTrait[]): Entity {
            const ctx = world[$internal];
            const entity = reserveEntity(ctx.entityIndex);
            ctx.deferred.reserved.add(entity);
            enqueue(world, { type: 'spawn', entity, ops: createAddOps(world, entity, traits) });
            return entity;
        },

        destroy(entity: Entity) {
            enqueue(world, { type: 'destroy', entity });
        },

        add(entity: Entity, ...traits: ConfigurableTrait[]) {
            if (traits.length === 0) return;
            enqueue(world, { type: 'mutate', entity, ops: createAddOps(world, entity, traits) });
        },

        remove(entity: Entity, ...traits: (Trait | RelationPair)[]) {
            if (traits.length === 0) return;
            enqueue(world, { type: 'mutate', entity, ops: createRemoveOps(traits) });
        },

        addExclusive(entity: Entity, pair: RelationPair) {
            enqueue(world, { type: 'mutate', entity, ops: [createExclusiveOp(pair)] });
        },

        flush() {
            flushBuffer(world, world[$internal].deferred.buffers.length - 1);
        },
    };
}

export function resetDeferred(world: World) {
    const deferred = world[$internal].deferred;
    for (const buffer of deferred.buffers) buffer.length = 0;
    deferred.size = 0;
    deferred.destroys = 0;
    deferred.subjects.clear();
    deferred.reserved.clear();
    deferred.view = null;
}

/**
 * Commands deferred until the matching `closeDeferredScope` are buffered separately,
 * so they can execute without touching the commands of outer scopes.
 */
export function openDeferredScope(world: World) {
    world[$internal].deferred.buffers.push([]);
}

/**
 * Executes the commands of the innermost scope when `flush` is true. Commands that are
 * left over, for example because the scope ended with an error, move to the outer scope.
 */
export function closeDeferredScope(world: World, flush: boolean) {
    const deferred = world[$internal].deferred;
    const level = deferred.buffers.length - 1;

    try {
        if (flush) flushBuffer(world, level);
    } finally {
        const leftover = deferred.buffers.pop()!;
        if (leftover.length > 0) deferred.buffers[level - 1].push(...leftover);
    }
}

/**
 * Must run before a non-deferred mutation. Commands pending for the entity execute first
 * so they keep their order relative to the mutation.
 */
export function beforeMutation(
    world: World,
    entity: Entity | undefined,
    kind: 'value' | 'structure' | 'destroy'
) {
    const deferred = world[$internal].deferred;
    if (deferred.size === 0) return;

    if (entity !== undefined && deferred.subjects.has(entity)) {
        flushEntity(world, entity);
        return;
    }

    // Other entities can only affect the cached view through destruction and its cascades.
    const view = deferred.view;
    if (view && (kind === 'destroy' || (kind === 'structure' && view.hasDestroys))) {
        deferred.view = null;
    }
}

/**
 * Returns the state the entity will have after the pending commands execute, or
 * undefined when the pending commands do not affect it.
 */
export function getDeferredView(world: World, entity: Entity): DeferredView | undefined {
    const deferred = world[$internal].deferred;
    if (deferred.size === 0) return undefined;
    if (deferred.destroys === 0 && !deferred.subjects.has(entity)) return undefined;

    if (!deferred.view) {
        const simulation = createSimulation(world);
        for (const buffer of deferred.buffers) {
            for (const command of buffer) simulateCommand(simulation, command);
        }
        deferred.view = simulation;
    }

    return deferred.view.views.get(entity);
}

export function deferredHas(world: World, view: DeferredView, param: Trait | RelationPair): boolean {
    if (!view.alive) return false;

    if (isRelationPair(param)) {
        const { relation, target } = param[$internal];
        const slot = view.relations.get(relation);
        if (!slot) return view.materialized && hasRelationPair(world, view.entity, param);
        return target === '*' ? slot.targets.size > 0 : slot.targets.has(target);
    }

    const slot = view.traits.get(param);
    if (!slot) return view.materialized && hasTrait(world, view.entity, param);
    return slot.present;
}

export function deferredGet(world: World, view: DeferredView, param: Trait | RelationPair): unknown {
    if (!view.alive) return undefined;

    if (isRelationPair(param)) {
        const { relation, target } = param[$internal];
        const slot = view.relations.get(relation);
        if (!slot) return view.materialized ? getTrait(world, view.entity, param) : undefined;
        if (typeof target !== 'number') return undefined;

        const pair = slot.targets.get(target);
        if (!pair) return undefined;
        if (!pair.buffered) return getRelationData(world, view.entity, relation, target);

        // Match the shape of getRelationData, which rebuilds SoA records from the store.
        const type = relation[$internal].trait[$internal].type;
        return type === 'aos' ? pair.value : { ...(pair.value as object) };
    }

    const slot = view.traits.get(param);
    if (!slot) return view.materialized ? getTrait(world, view.entity, param) : undefined;
    if (!slot.present) return undefined;
    if (!slot.buffered) return getTrait(world, view.entity, param);

    const type = param[$internal].type;
    if (type === 'tag') return undefined;
    return type === 'aos' ? slot.value : { ...(slot.value as object) };
}

/* Recording */

function enqueue(world: World, command: DeferredCommand) {
    const deferred = world[$internal].deferred;
    deferred.buffers[deferred.buffers.length - 1].push(command);
    track(deferred, command, 1);
    if (deferred.view) simulateCommand(deferred.view, command);
}

function track(deferred: DeferredInternal, command: DeferredCommand, delta: 1 | -1) {
    deferred.size += delta;
    if (command.type === 'destroy') deferred.destroys += delta;

    const count = (deferred.subjects.get(command.entity) ?? 0) + delta;
    if (count > 0) deferred.subjects.set(command.entity, count);
    else deferred.subjects.delete(command.entity);
}

// Values are created when the command is recorded so `get` returns what the flush stores.
function createAddOps(world: World, entity: Entity, configs: ConfigurableTrait[]): DeferredOp[] {
    const ops: DeferredOp[] = [];

    for (const config of configs) {
        if (isRelationPair(config)) {
            const { relation, target, params } = config[$internal];
            if (typeof target !== 'number') continue;
            ops.push({
                type: 'addPair',
                relation,
                target,
                value: createPairValue(relation, params),
                hasParams: params != null,
            });
            continue;
        }

        const [trait, params] = Array.isArray(config) ? config : [config, undefined];
        ops.push({
            type: 'add',
            trait,
            value: createTraitValue(world, entity, trait, params),
            hasParams: params != null,
        });
    }

    return ops;
}

function createRemoveOps(traits: (Trait | RelationPair)[]): DeferredOp[] {
    const ops: DeferredOp[] = [];

    for (const trait of traits) {
        if (isRelationPair(trait)) {
            const { relation, target } = trait[$internal];
            if (target === '*') ops.push({ type: 'clearPairs', relation });
            else if (typeof target === 'number') ops.push({ type: 'removePair', relation, target });
            continue;
        }

        const relation = trait[$internal].relation;
        ops.push(relation ? { type: 'clearPairs', relation } : { type: 'remove', trait });
    }

    return ops;
}

function createExclusiveOp(pair: RelationPair): DeferredOp {
    const { relation, target, params } = pair[$internal];
    if (target === '*') return { type: 'clearPairs', relation };

    return {
        type: 'exclusivePair',
        relation,
        target,
        value: createPairValue(relation, params),
        hasParams: params != null,
    };
}

function createTraitValue(world: World, entity: Entity, trait: Trait, params: unknown): unknown {
    const type = trait[$internal].type;
    if (type === 'tag') return undefined;

    if (type === 'aos') {
        if (params != null) return params;
        return isOrderedTrait(trait)
            ? new OrderedList(world, entity, getOrderedTraitRelation(trait), trait)
            : (trait.schema as () => unknown)();
    }

    return createRecord(trait.schema, params as Record<string, unknown> | undefined);
}

function createPairValue(relation: Relation<Trait>, params?: Record<string, unknown>): unknown {
    const trait = relation[$internal].trait;
    const type = trait[$internal].type;
    if (type === 'tag') return undefined;
    if (type === 'aos') return { ...getSchemaDefaults(trait.schema, type), ...params };
    return createRecord(trait.schema, params);
}

/** Merges params over the schema defaults, keeping only schema keys like the SoA store does. */
function createRecord(schema: Record<string, unknown>, params?: Record<string, unknown>) {
    const defaults = getSchemaDefaults(schema, 'soa')!;
    const record: Record<string, unknown> = {};
    for (const key in defaults) {
        record[key] = params && key in params ? params[key] : defaults[key];
    }
    return record;
}

/* Execution */

function flushBuffer(world: World, level: number) {
    const deferred = world[$internal].deferred;

    // Subscribers may defer more commands while this buffer executes.
    while (deferred.buffers[level].length > 0) {
        const commands = deferred.buffers[level];
        deferred.buffers[level] = [];
        for (const command of commands) track(deferred, command, -1);
        deferred.view = null;

        const runnable = level > 0 ? deferPinnedCommands(world, commands, level - 1) : commands;
        executeCommands(world, runnable, level);
    }
}

function flushEntity(world: World, entity: Entity) {
    const deferred = world[$internal].deferred;

    while (deferred.subjects.has(entity)) {
        const level = deferred.buffers.findIndex((buffer) =>
            buffer.some((command) => command.entity === entity)
        );
        if (level === -1) return;
        for (let i = level; i < deferred.buffers.length; i++) flushBuffer(world, i);
    }
}

/**
 * An inner scope cannot execute commands on entities spawned by an outer scope, since
 * they do not exist yet. Those commands, and later ones touching the same entities, move
 * to the outer scope so their relative order is kept.
 */
function deferPinnedCommands(
    world: World,
    commands: DeferredCommand[],
    outerLevel: number
): DeferredCommand[] {
    const deferred = world[$internal].deferred;
    if (deferred.reserved.size === 0) return commands;

    const spawned = new Set<Entity>();
    for (const command of commands) {
        if (command.type === 'spawn') spawned.add(command.entity);
    }

    const pinned = new Set<Entity>();
    for (const entity of deferred.reserved) {
        if (!spawned.has(entity)) pinned.add(entity);
    }
    if (pinned.size === 0) return commands;

    const runnable: DeferredCommand[] = [];
    for (const command of commands) {
        const entities = getCommandEntities(command);
        if (!entities.some((entity) => pinned.has(entity))) {
            runnable.push(command);
            continue;
        }

        for (const entity of entities) pinned.add(entity);
        deferred.buffers[outerLevel].push(command);
        track(deferred, command, 1);
    }

    return runnable;
}

function getCommandEntities(command: DeferredCommand): Entity[] {
    const entities = [command.entity];
    if (command.type === 'destroy') return entities;

    for (const op of command.ops) {
        if (op.type === 'addPair' || op.type === 'exclusivePair' || op.type === 'removePair') {
            entities.push(op.target);
        }
    }
    return entities;
}

function executeCommands(world: World, commands: DeferredCommand[], level: number) {
    const simulation = createSimulation(world);

    let abortedAt = -1;
    for (let i = 0; i < commands.length; i++) {
        simulateCommand(simulation, commands[i]);
        if (simulation.aborted) {
            abortedAt = i;
            break;
        }
    }

    applySimulation(world, simulation);
    if (abortedAt === -1) return;

    // Only the offending command is dropped. The rest stay queued ahead of newer commands.
    const deferred = world[$internal].deferred;
    const remaining = commands.slice(abortedAt + 1);
    for (const command of remaining) track(deferred, command, 1);
    deferred.buffers[level].unshift(...remaining);
    deferred.view = null;

    throw new Error(worldEntityError);
}

/**
 * Applies only the net difference of a simulation to the world. Intermediate states
 * never reach the world, so subscriptions and queries observe each change once.
 */
function applySimulation(world: World, simulation: DeferredSimulation) {
    const ctx = world[$internal];
    const index = ctx.entityIndex;

    // Spawned entities exist before anything can point at them. Entities that were
    // spawned and destroyed by the same commands are never made alive.
    for (const effect of simulation.effects) {
        if (effect.type !== 'spawn') continue;
        const { entity, alive } = effect.view;
        if (!ctx.deferred.reserved.delete(entity)) continue;

        if (alive) {
            activateReservedEntity(index, entity);
            initEntity(world, entity);
        } else {
            releaseReservedEntity(index, entity);
        }
    }

    for (const effect of simulation.effects) {
        if (effect.type === 'trait') {
            applyTraitSlot(world, effect.slot);
        } else if (effect.type === 'relation') {
            applyRelationSlot(world, effect.slot);
        } else if (effect.type === 'destroy') {
            const { entity, materialized } = effect.view;
            // The simulation already resolved cascades, including nullified spawns.
            if (materialized && isEntityAlive(index, entity)) destroyEntity(world, entity, false);
        }
    }
}

function applyTraitSlot(world: World, slot: DeferredTraitSlot) {
    const { view, trait } = slot;
    const entity = view.entity;
    if (!view.alive || !isEntityAlive(world[$internal].entityIndex, entity)) return;

    const type = trait[$internal].type;
    const has = hasTrait(world, entity, trait);

    if (!slot.present) {
        if (has) removeTrait(world, entity, trait);
        return;
    }

    if (!has) {
        const config = slot.buffered && type !== 'tag' ? [trait, slot.value] : trait;
        addTrait(world, entity, config as ConfigurableTrait);
        return;
    }

    // The trait was removed and added again, so its value was reinitialized.
    if (slot.buffered && type !== 'tag') {
        if (!isSameRecord(type, getTrait(world, entity, trait), slot.value)) {
            setTrait(world, entity, trait, slot.value);
        }
    }
}

function applyRelationSlot(world: World, slot: DeferredRelationSlot) {
    const { view, relation, targets } = slot;
    const entity = view.entity;
    const index = world[$internal].entityIndex;
    if (!view.alive || !isEntityAlive(index, entity)) return;

    const type = relation[$internal].trait[$internal].type;
    const current = getRelationTargets(world, relation, entity);

    // Add before removing so the relation trait is not removed and added back.
    for (const [target, pair] of targets) {
        if (current.includes(target)) {
            if (pair.buffered && type !== 'tag') {
                const data = getRelationData(world, entity, relation, target);
                if (!isSameRecord(type, data, pair.value)) {
                    setTrait(world, entity, relation(target), pair.value);
                }
            }
        } else if (isEntityAlive(index, target)) {
            const value = pair.value as Record<string, unknown> | undefined;
            addTrait(world, entity, relation(target, value));
            // Adding copies AoS values over the defaults. Store the recorded value itself.
            if (type === 'aos' && pair.buffered) setRelationData(world, entity, relation, target, value!);
        }
    }

    for (const target of current) {
        if (targets.has(target) || !hasRelationToTarget(world, relation, entity, target)) continue;
        removeTrait(world, entity, relation(target));
    }
}

function isSameRecord(type: StoreType, a: unknown, b: unknown): boolean {
    return type === 'aos' ? a === b : shallowEqual(a, b);
}

/* Simulation */

function createSimulation(world: World): DeferredSimulation {
    return {
        world,
        views: new Map(),
        effects: [],
        relationSlots: new Map(),
        hasDestroys: false,
        aborted: false,
    };
}

function createView(entity: Entity, alive: boolean, materialized: boolean): DeferredView {
    return { entity, alive, materialized, traits: new Map(), relations: new Map() };
}

function isSimulatedAlive(simulation: DeferredSimulation, entity: Entity): boolean {
    const view = simulation.views.get(entity);
    return view ? view.alive : isEntityAlive(simulation.world[$internal].entityIndex, entity);
}

function getSimulatedView(simulation: DeferredSimulation, entity: Entity): DeferredView {
    let view = simulation.views.get(entity);
    if (!view) {
        const alive = isEntityAlive(simulation.world[$internal].entityIndex, entity);
        view = createView(entity, alive, alive);
        simulation.views.set(entity, view);
    }
    return view;
}

function simulateCommand(simulation: DeferredSimulation, command: DeferredCommand) {
    if (command.type === 'destroy') {
        simulateDestroy(simulation, command.entity);
        return;
    }

    let view: DeferredView;
    if (command.type === 'spawn') {
        view = createView(command.entity, true, false);
        simulation.views.set(command.entity, view);
        simulation.effects.push({ type: 'spawn', view });
    } else {
        if (!isSimulatedAlive(simulation, command.entity)) return;
        view = getSimulatedView(simulation, command.entity);
    }

    for (const op of command.ops) simulateOp(simulation, view, op);
}

function simulateOp(simulation: DeferredSimulation, view: DeferredView, op: DeferredOp) {
    switch (op.type) {
        case 'add': {
            const slot = getTraitSlot(simulation, view, op.trait);
            if (!slot.present) {
                slot.present = true;
                slot.buffered = true;
                slot.value = op.value;
            } else if (slot.buffered && op.hasParams) {
                slot.value = op.value;
            }
            break;
        }
        case 'remove': {
            const slot = getTraitSlot(simulation, view, op.trait);
            slot.present = false;
            slot.buffered = false;
            slot.value = undefined;
            break;
        }
        case 'addPair':
            simulateAddPair(simulation, view, op.relation, op.target, op.value, op.hasParams);
            break;
        case 'exclusivePair': {
            if (!isSimulatedAlive(simulation, op.target)) break;
            const slot = getRelationSlot(simulation, view, op.relation);
            for (const target of slot.targets.keys()) {
                if (target !== op.target) slot.targets.delete(target);
            }
            simulateAddPair(simulation, view, op.relation, op.target, op.value, op.hasParams);
            break;
        }
        case 'removePair':
            getRelationSlot(simulation, view, op.relation).targets.delete(op.target);
            break;
        case 'clearPairs':
            getRelationSlot(simulation, view, op.relation).targets.clear();
            break;
    }
}

function simulateAddPair(
    simulation: DeferredSimulation,
    view: DeferredView,
    relation: Relation<Trait>,
    target: Entity,
    value: unknown,
    hasParams: boolean
) {
    if (!isSimulatedAlive(simulation, target)) return;

    const slot = getRelationSlot(simulation, view, relation);
    if (relation[$internal].exclusive) {
        for (const existing of slot.targets.keys()) {
            if (existing !== target) slot.targets.delete(existing);
        }
    }

    const pair = slot.targets.get(target);
    if (!pair) slot.targets.set(target, { buffered: true, value });
    else if (pair.buffered && hasParams) pair.value = value;
}

function simulateDestroy(simulation: DeferredSimulation, root: Entity) {
    const ctx = simulation.world[$internal];
    const relations = new Set([...ctx.relations, ...simulation.relationSlots.keys()]);
    const destroyed = new Set<Entity>();
    const order: Entity[] = [];
    const cleanups: [source: Entity, relation: Relation<Trait>, target: Entity][] = [];
    const queue = [root];

    // Resolve the whole cascade before changing anything so an invalid destroy has no effect.
    while (queue.length > 0) {
        const entity = queue.pop()!;
        if (destroyed.has(entity) || !isSimulatedAlive(simulation, entity)) continue;

        if (entity === ctx.worldEntity) {
            simulation.aborted = true;
            return;
        }

        destroyed.add(entity);
        order.push(entity);

        for (const relation of relations) {
            const { autoDestroy } = relation[$internal];

            for (const source of getSimulatedSources(simulation, relation, entity)) {
                cleanups.push([source, relation, entity]);
                if (autoDestroy === 'source') queue.push(source);
            }

            if (autoDestroy === 'target') {
                queue.push(...getSimulatedTargets(simulation, relation, entity));
            }
        }
    }

    for (const entity of order) {
        const view = getSimulatedView(simulation, entity);
        view.alive = false;
        simulation.effects.push({ type: 'destroy', view });
    }

    for (const [source, relation, target] of cleanups) {
        if (destroyed.has(source)) continue;
        getRelationSlot(simulation, getSimulatedView(simulation, source), relation).targets.delete(
            target
        );
    }

    simulation.hasDestroys = true;
}

function getSimulatedSources(
    simulation: DeferredSimulation,
    relation: Relation<Trait>,
    target: Entity
): Entity[] {
    const sources: Entity[] = [];

    for (const slot of simulation.relationSlots.get(relation) ?? []) {
        if (slot.view.alive && slot.targets.has(target)) sources.push(slot.view.entity);
    }

    for (const source of getEntitiesWithRelationTo(simulation.world, relation, target)) {
        const view = simulation.views.get(source);
        // Simulated relations were already checked above.
        if (view && (!view.alive || view.relations.has(relation))) continue;
        sources.push(source);
    }

    return sources;
}

function getSimulatedTargets(
    simulation: DeferredSimulation,
    relation: Relation<Trait>,
    source: Entity
): readonly Entity[] {
    const view = simulation.views.get(source);
    const slot = view?.relations.get(relation);
    if (slot) return [...slot.targets.keys()];
    if (view && !view.materialized) return [];
    return getRelationTargets(simulation.world, relation, source);
}

function getTraitSlot(
    simulation: DeferredSimulation,
    view: DeferredView,
    trait: Trait
): DeferredTraitSlot {
    let slot = view.traits.get(trait);
    if (!slot) {
        const present = view.materialized && hasTrait(simulation.world, view.entity, trait);
        slot = { view, trait, present, buffered: false, value: undefined };
        view.traits.set(trait, slot);
        simulation.effects.push({ type: 'trait', slot });
    }
    return slot;
}

function getRelationSlot(
    simulation: DeferredSimulation,
    view: DeferredView,
    relation: Relation<Trait>
): DeferredRelationSlot {
    let slot = view.relations.get(relation);
    if (!slot) {
        const targets = new Map();
        if (view.materialized) {
            for (const target of getRelationTargets(simulation.world, relation, view.entity)) {
                targets.set(target, unbufferedPair);
            }
        }

        slot = { view, relation, targets };
        view.relations.set(relation, slot);

        let slots = simulation.relationSlots.get(relation);
        if (!slots) simulation.relationSlots.set(relation, (slots = []));
        slots.push(slot);

        simulation.effects.push({ type: 'relation', slot });
    }
    return slot;
}

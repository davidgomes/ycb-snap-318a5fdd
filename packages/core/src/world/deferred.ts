import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { destroyEntity } from '../entity/entity';
import { allocateEntity, getAliveEntities, isEntityAlive, releaseEntity } from '../entity/utils/entity-index';
import { getEntityId } from '../entity/utils/pack-entity';
import type { QueryInstance } from '../query/types';
import {
    getRelationData,
    getRelationTargets,
    hasRelationToTarget,
} from '../relation/relation';
import type { Relation, RelationPair } from '../relation/types';
import { isRelationPair } from '../relation/utils/is-relation';
import { getSchemaDefaults } from '../storage';
import { shallowEqual } from '../utils/shallow-equal';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import { getTraitInstance } from '../trait/trait-instance';
import type { ConfigurableTrait, Trait } from '../trait/types';
import type { DeferredCommand, DeferredSubEvent, World } from './types';

type DeferredBuffer = { commands: DeferredCommand[] };

type TraitSlot = { fromWorld: boolean; params?: unknown; value?: unknown };
type VirtualState = {
    alive: boolean;
    traits: Map<Trait, TraitSlot>;
    relations: Map<Relation, Map<Entity, TraitSlot>>;
};

const queryIds = new WeakMap<QueryInstance, number>();
let nextQueryId = 1;

function queryId(query: QueryInstance): number {
    let id = queryIds.get(query);
    if (!id) {
        id = nextQueryId++;
        queryIds.set(query, id);
    }
    return id;
}

export function createDeferredBuffer(): DeferredBuffer {
    return { commands: [] };
}

export function hasDeferredCommands(world: World): boolean {
    const stack = world[$internal].deferredStack;
    for (let i = 0; i < stack.length; i++) {
        if (stack[i].commands.length > 0) return true;
    }
    return false;
}

export function clearDeferredCommands(world: World): void {
    const ctx = world[$internal];
    ctx.deferredStack = [createDeferredBuffer()];
    ctx.deferredSubLog = null;
    ctx.deferredFlushing = false;
}

export function beginDeferredScope(world: World): void {
    world[$internal].deferredStack.push(createDeferredBuffer());
}

export function endDeferredScope(world: World): void {
    const stack = world[$internal].deferredStack;
    if (stack.length <= 1) return;
    const buffer = stack.pop()!;
    flushCommandList(world, buffer.commands);
}

function currentBuffer(world: World): DeferredBuffer {
    const stack = world[$internal].deferredStack;
    return stack[stack.length - 1];
}

function entityHasCommands(world: World, entity: Entity): boolean {
    const stack = world[$internal].deferredStack;
    for (let i = 0; i < stack.length; i++) {
        const commands = stack[i].commands;
        for (let j = 0; j < commands.length; j++) {
            if (commands[j].entity === entity) return true;
        }
    }
    return false;
}

export function flushDeferredForEntity(world: World, entity: Entity): void {
    const ctx = world[$internal];
    if (ctx.deferredFlushing) return;
    if (!entityHasCommands(world, entity)) return;

    const extracted: DeferredCommand[] = [];
    for (let i = 0; i < ctx.deferredStack.length; i++) {
        const commands = ctx.deferredStack[i].commands;
        let write = 0;
        for (let j = 0; j < commands.length; j++) {
            const command = commands[j];
            if (command.entity === entity) extracted.push(command);
            else commands[write++] = command;
        }
        commands.length = write;
    }

    flushCommandList(world, extracted);
}

export function flushDeferred(world: World): void {
    const buffer = currentBuffer(world);
    const commands = buffer.commands.splice(0, buffer.commands.length);
    flushCommandList(world, commands);
}

function flushCommandList(world: World, commands: DeferredCommand[]): void {
    if (commands.length === 0) return;
    const ctx = world[$internal];
    if (ctx.deferredFlushing) {
        currentBuffer(world).commands.push(...commands);
        return;
    }

    ctx.deferredFlushing = true;
    const log: DeferredSubEvent[] = [];
    ctx.deferredSubLog = log;
    const before = captureAlive(world);
    let thrown: unknown;
    try {
        executeCommands(world, commands);
    } catch (error) {
        thrown = error;
    } finally {
        ctx.deferredSubLog = null;
        ctx.deferredFlushing = false;
    }

    if (thrown === undefined) {
        emitStateDiff(world, before, captureAlive(world));
        replayQuerySubscriptions(log);
    }
    if (thrown !== undefined) throw thrown;
}

function nullifiedEntities(commands: DeferredCommand[]): Set<Entity> {
    const spawned = new Set<Entity>();
    const nullified = new Set<Entity>();
    for (let i = 0; i < commands.length; i++) {
        const command = commands[i];
        if (command.type === 'spawn') spawned.add(command.entity);
        else if (command.type === 'destroy' && spawned.has(command.entity)) {
            nullified.add(command.entity);
            spawned.delete(command.entity);
        }
    }
    return nullified;
}

function executeCommands(world: World, commands: DeferredCommand[]): void {
    const nullified = nullifiedEntities(commands);
    const ctx = world[$internal];

    for (let i = 0; i < commands.length; i++) {
        const command = commands[i];
        if (nullified.has(command.entity)) continue;

        if (command.type === 'destroy' && command.entity === ctx.worldEntity) {
            throw new Error('Koota: Cannot destroy the world entity.');
        }

        if (command.type !== 'spawn' && !isEntityAlive(ctx.entityIndex, command.entity)) continue;

        if (command.type === 'spawn') {
            if (!isEntityAlive(ctx.entityIndex, command.entity)) continue;
            executeSpawn(world, command.entity, command.traits);
        } else if (command.type === 'destroy') {
            destroyEntity(world, command.entity);
        } else if (command.type === 'add') {
            for (let j = 0; j < command.traits.length; j++) {
                applyConfigurable(world, command.entity, command.traits[j]);
            }
        } else if (command.type === 'remove') {
            removeTrait(world, command.entity, ...command.traits);
        } else {
            applyAddExclusive(world, command.entity, command.pair);
        }
    }

    for (const entity of nullified) discardReserved(world, entity);
}

function executeSpawn(world: World, entity: Entity, traits: ConfigurableTrait[]): void {
    const ctx = world[$internal];
    for (const query of ctx.notQueries) {
        const match = query.check(world, entity);
        if (match) query.add(entity);
        query.resetTrackingBitmasks(getEntityId(entity));
    }
    if (traits.length > 0) addTrait(world, entity, ...traits);
}

function discardReserved(world: World, entity: Entity): void {
    const ctx = world[$internal];
    if (!isEntityAlive(ctx.entityIndex, entity)) return;

    for (const query of ctx.notQueries) {
        if (query.entities.has(entity)) query.remove(world, entity);
    }
    const allQuery = ctx.queriesHashMap.get('');
    if (allQuery && allQuery.entities.has(entity)) allQuery.remove(world, entity);

    ctx.entityTraits.delete(entity);
    const eid = getEntityId(entity);
    for (let i = 0; i < ctx.entityMasks.length; i++) ctx.entityMasks[i][eid] = 0;
    releaseEntity(ctx.entityIndex, entity);
}

function normalizeTrait(config: ConfigurableTrait): { trait: Trait; params?: unknown } | RelationPair {
    if (isRelationPair(config)) return config;
    if (Array.isArray(config)) {
        const [trait, params] = config as [Trait, unknown];
        return { trait, params };
    }
    return { trait: config as Trait };
}

function replacementValue(trait: Trait, params: unknown): unknown {
    const defaults = getSchemaDefaults(trait.schema, trait[$internal].type);
    if (trait[$internal].type === 'aos') return params ?? defaults;
    if (defaults) return { ...defaults, ...(params as object | undefined) };
    return params;
}

function applyConfigurable(world: World, entity: Entity, config: ConfigurableTrait): void {
    const normalized = normalizeTrait(config);
    if (isRelationPair(normalized)) {
        const pairCtx = normalized[$internal];
        const target = pairCtx.target;
        if (typeof target !== 'number') return;
        const relation = pairCtx.relation;
        if (hasRelationToTarget(world, relation, entity, target)) {
            if (pairCtx.params) {
                setTrait(
                    world,
                    entity,
                    normalized,
                    replacementValue(relation[$internal].trait, pairCtx.params),
                    true
                );
            }
            return;
        }
        addTrait(world, entity, normalized);
        return;
    }

    const { trait, params } = normalized;
    if (trait[$internal].relation) {
        addTrait(world, entity, config);
        return;
    }

    if (hasTrait(world, entity, trait)) {
        if (params !== undefined) setTrait(world, entity, trait, replacementValue(trait, params), true);
        return;
    }

    addTrait(world, entity, config);
}

function applyAddExclusive(world: World, entity: Entity, pair: RelationPair): void {
    const pairCtx = pair[$internal];
    const relation = pairCtx.relation;
    const target = pairCtx.target;
    const existing = getRelationTargets(world, relation, entity);
    for (let i = 0; i < existing.length; i++) {
        removeTrait(world, entity, relation(existing[i]));
    }
    if (typeof target === 'number') addTrait(world, entity, pair);
}

function reserveEntity(world: World): Entity {
    const ctx = world[$internal];
    const entity = allocateEntity(ctx.entityIndex);
    ctx.entityTraits.set(entity, new Set());
    const eid = getEntityId(entity);
    for (let i = 0; i < ctx.entityMasks.length; i++) {
        if (ctx.entityMasks[i][eid] === undefined) ctx.entityMasks[i][eid] = 0;
    }
    return entity;
}

export function createDeferredApi(world: World) {
    return {
        spawn(...traits: ConfigurableTrait[]): Entity {
            const entity = reserveEntity(world);
            currentBuffer(world).commands.push({ type: 'spawn', entity, traits });
            return entity;
        },
        destroy(entity: Entity): void {
            currentBuffer(world).commands.push({ type: 'destroy', entity });
        },
        add(entity: Entity, ...traits: ConfigurableTrait[]): void {
            currentBuffer(world).commands.push({ type: 'add', entity, traits });
        },
        remove(entity: Entity, ...traits: (Trait | RelationPair)[]): void {
            currentBuffer(world).commands.push({ type: 'remove', entity, traits });
        },
        addExclusive(entity: Entity, pair: RelationPair): void {
            currentBuffer(world).commands.push({ type: 'addExclusive', entity, pair });
        },
        flush(): void {
            flushDeferred(world);
        },
    };
}

type EntityCapture = {
    traits: Map<Trait, unknown>;
    pairs: Map<Relation, Map<Entity, unknown>>;
};

function captureAlive(world: World): Map<Entity, EntityCapture> {
    const captured = new Map<Entity, EntityCapture>();
    const alive = getAliveEntities(world[$internal].entityIndex);
    for (let i = 0; i < alive.length; i++) captured.set(alive[i], captureEntity(world, alive[i]));
    return captured;
}

function captureEntity(world: World, entity: Entity): EntityCapture {
    const traits = new Map<Trait, unknown>();
    const pairs = new Map<Relation, Map<Entity, unknown>>();
    const owned = world[$internal].entityTraits.get(entity);
    if (!owned) return { traits, pairs };

    for (const trait of owned) {
        const relation = trait[$internal].relation as Relation | null;
        if (relation) {
            const targets = getRelationTargets(world, relation, entity);
            const map = new Map<Entity, unknown>();
            for (let i = 0; i < targets.length; i++) {
                map.set(targets[i], getRelationData(world, entity, relation, targets[i]));
            }
            pairs.set(relation, map);
        } else {
            traits.set(trait, getTrait(world, entity, trait));
        }
    }

    return { traits, pairs };
}

function valuesEqual(prev: unknown, next: unknown): boolean {
    if (prev === next) return true;
    if (typeof prev === 'object' && prev && typeof next === 'object' && next) {
        return shallowEqual(prev as Record<string, unknown>, next as Record<string, unknown>);
    }
    return false;
}

function emitAdd(world: World, trait: Trait, entity: Entity, target?: Entity): void {
    const instance = getTraitInstance(world[$internal].traitInstances, trait);
    if (!instance) return;
    for (const sub of instance.addSubscriptions) sub(entity, target);
}

function emitRemove(world: World, trait: Trait, entity: Entity, target?: Entity): void {
    const instance = getTraitInstance(world[$internal].traitInstances, trait);
    if (!instance) return;
    for (const sub of instance.removeSubscriptions) sub(entity, target);
}

function emitChange(world: World, trait: Trait, entity: Entity, target?: Entity): void {
    const instance = getTraitInstance(world[$internal].traitInstances, trait);
    if (!instance) return;
    for (const sub of instance.changeSubscriptions) sub(entity, target);
}

function emitStateDiff(
    world: World,
    before: Map<Entity, EntityCapture>,
    after: Map<Entity, EntityCapture>
): void {
    const entities = new Set<Entity>([...before.keys(), ...after.keys()]);
    for (const entity of entities) {
        const prev = before.get(entity) ?? { traits: new Map(), pairs: new Map() };
        const next = after.get(entity) ?? { traits: new Map(), pairs: new Map() };

        for (const [trait] of prev.traits) {
            if (!next.traits.has(trait)) emitRemove(world, trait, entity);
        }
        for (const [trait, value] of next.traits) {
            if (!prev.traits.has(trait)) emitAdd(world, trait, entity);
            else if (!valuesEqual(prev.traits.get(trait), value)) emitChange(world, trait, entity);
        }

        const relations = new Set<Relation>([...prev.pairs.keys(), ...next.pairs.keys()]);
        for (const relation of relations) {
            const trait = relation[$internal].trait;
            const prevPairs = prev.pairs.get(relation) ?? new Map<Entity, unknown>();
            const nextPairs = next.pairs.get(relation) ?? new Map<Entity, unknown>();
            for (const [target] of prevPairs) {
                if (!nextPairs.has(target)) emitRemove(world, trait, entity, target);
            }
            for (const [target, value] of nextPairs) {
                if (!prevPairs.has(target)) emitAdd(world, trait, entity, target);
                else if (!valuesEqual(prevPairs.get(target), value)) emitChange(world, trait, entity, target);
            }
        }
    }
}

function replayQuerySubscriptions(log: DeferredSubEvent[]): void {
    if (log.length === 0) return;

    type Group = { index: number; adds: number; removes: number; event: DeferredSubEvent };
    const groups = new Map<string, Group>();

    for (let i = 0; i < log.length; i++) {
        const event = log[i];
        if (event.kind !== 'query-add' && event.kind !== 'query-remove') continue;
        const key = `q:${queryId(event.query)}:${event.entity}`;
        let group = groups.get(key);
        if (!group) {
            group = { index: i, adds: 0, removes: 0, event };
            groups.set(key, group);
        }
        if (event.kind === 'query-add') group.adds++;
        else group.removes++;
    }

    const ordered = [...groups.values()].sort((a, b) => a.index - b.index);
    for (let i = 0; i < ordered.length; i++) {
        const group = ordered[i];
        const event = group.event;
        if (event.kind !== 'query-add' && event.kind !== 'query-remove') continue;
        const net = group.adds - group.removes;
        if (net > 0) {
            for (const sub of event.query.addSubscriptions) sub(event.entity);
        } else if (net < 0) {
            for (const sub of event.query.removeSubscriptions) sub(event.entity);
        }
    }
}

function copyState(world: World, entity: Entity): VirtualState {
    const ctx = world[$internal];
    const state: VirtualState = { alive: isEntityAlive(ctx.entityIndex, entity), traits: new Map(), relations: new Map() };
    if (!state.alive) return state;

    const entityTraits = ctx.entityTraits.get(entity);
    if (!entityTraits) return state;

    for (const trait of entityTraits) {
        const relation = trait[$internal].relation as Relation | null;
        if (relation) {
            const targets = getRelationTargets(world, relation, entity);
            const map = new Map<Entity, TraitSlot>();
            for (let i = 0; i < targets.length; i++) {
                const target = targets[i];
                map.set(target, { fromWorld: true, value: getRelationData(world, entity, relation, target) });
            }
            state.relations.set(relation, map);
        } else {
            state.traits.set(trait, { fromWorld: true, value: getTrait(world, entity, trait) });
        }
    }

    return state;
}

function applyVirtualConfigurable(state: VirtualState, config: ConfigurableTrait): void {
    const normalized = normalizeTrait(config);
    if (isRelationPair(normalized)) {
        const pairCtx = normalized[$internal];
        const target = pairCtx.target;
        if (typeof target !== 'number') return;
        const relation = pairCtx.relation;
        let map = state.relations.get(relation);
        if (!map || relation[$internal].exclusive) map = new Map();
        map.set(target, { fromWorld: false, params: pairCtx.params });
        state.relations.set(relation, map);
        return;
    }

    state.traits.set(normalized.trait, { fromWorld: false, params: normalized.params });
}

function applyVirtualCommand(world: World, state: VirtualState, command: DeferredCommand): void {
    if (!state.alive && command.type !== 'spawn') return;

    if (command.type === 'spawn') {
        if (!isEntityAlive(world[$internal].entityIndex, command.entity)) return;
        state.alive = true;
        state.traits.clear();
        state.relations.clear();
        for (let i = 0; i < command.traits.length; i++) applyVirtualConfigurable(state, command.traits[i]);
        return;
    }

    if (command.type === 'destroy') {
        state.alive = false;
        state.traits.clear();
        state.relations.clear();
        return;
    }

    if (command.type === 'add') {
        for (let i = 0; i < command.traits.length; i++) applyVirtualConfigurable(state, command.traits[i]);
        return;
    }

    if (command.type === 'remove') {
        for (let i = 0; i < command.traits.length; i++) {
            const trait = command.traits[i];
            if (isRelationPair(trait)) {
                const pairCtx = trait[$internal];
                if (pairCtx.target === '*') state.relations.delete(pairCtx.relation);
                else if (typeof pairCtx.target === 'number') {
                    const map = state.relations.get(pairCtx.relation);
                    map?.delete(pairCtx.target);
                    if (map && map.size === 0) state.relations.delete(pairCtx.relation);
                }
            } else if ((trait as Trait)[$internal]?.relation) {
                state.relations.delete((trait as Trait)[$internal].relation!);
                state.traits.delete(trait as Trait);
            } else {
                state.traits.delete(trait as Trait);
            }
        }
        return;
    }

    const pairCtx = command.pair[$internal];
    if (pairCtx.target === '*' || typeof pairCtx.target !== 'number') {
        state.relations.set(pairCtx.relation, new Map());
        return;
    }
    const map = new Map<Entity, TraitSlot>();
    map.set(pairCtx.target, { fromWorld: false, params: pairCtx.params });
    state.relations.set(pairCtx.relation, map);
}

function previewState(world: World, entity: Entity): VirtualState | undefined {
    if (!hasDeferredCommands(world)) return undefined;
    const state = copyState(world, entity);
    const stack = world[$internal].deferredStack;

    for (let i = 0; i < stack.length; i++) {
        const commands = stack[i].commands;
        const nullified = nullifiedEntities(commands);
        if (nullified.has(entity)) {
            state.alive = false;
            state.traits.clear();
            state.relations.clear();
            continue;
        }
        for (let j = 0; j < commands.length; j++) {
            if (commands[j].entity === entity) applyVirtualCommand(world, state, commands[j]);
        }
    }

    return state;
}

function slotValue(trait: Trait, slot: TraitSlot): unknown {
    if (slot.fromWorld) return slot.value;
    if (slot.params === undefined && trait[$internal].type === 'tag') return undefined;
    return replacementValue(trait, slot.params);
}

export function previewHas(world: World, entity: Entity, trait: Trait | RelationPair): boolean | undefined {
    const state = previewState(world, entity);
    if (!state) return undefined;
    if (!state.alive) return false;

    if (isRelationPair(trait)) {
        const pairCtx = trait[$internal];
        const map = state.relations.get(pairCtx.relation);
        if (!map || map.size === 0) return false;
        if (pairCtx.target === '*') return true;
        return typeof pairCtx.target === 'number' && map.has(pairCtx.target);
    }

    const relation = (trait as Trait)[$internal].relation as Relation | null;
    if (relation) {
        const map = state.relations.get(relation);
        return !!map && map.size > 0;
    }

    return state.traits.has(trait as Trait);
}

export function previewGet(world: World, entity: Entity, trait: Trait | RelationPair): unknown {
    const state = previewState(world, entity);
    if (!state) return undefined;
    if (!state.alive) return undefined;

    if (isRelationPair(trait)) {
        const pairCtx = trait[$internal];
        if (typeof pairCtx.target !== 'number') return undefined;
        const slot = state.relations.get(pairCtx.relation)?.get(pairCtx.target);
        if (!slot) return undefined;
        return slotValue(pairCtx.relation[$internal].trait, slot);
    }

    const slot = state.traits.get(trait as Trait);
    if (!slot) return undefined;
    return slotValue(trait as Trait, slot);
}

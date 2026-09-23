import { $internal } from '../common';
import { destroyEntity, initEntity } from '../entity/entity';
import type { Entity } from '../entity/types';
import { allocateEntity, isEntityAlive, releaseEntity } from '../entity/utils/entity-index';
import { getEntityId } from '../entity/utils/pack-entity';
import { setChanged, setPairChanged } from '../query/modifiers/changed';
import type { QueryInstance } from '../query/types';
import {
    getEntitiesWithRelationTo,
    getRelationData,
    getRelationTargets,
    setRelationData,
} from '../relation/relation';
import type { Relation, RelationPair } from '../relation/types';
import { isRelationPair } from '../relation/utils/is-relation';
import { getSchemaDefaults } from '../storage';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import { getTraitInstance } from '../trait/trait-instance';
import type { ConfigurableTrait, Trait } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { Deferred, DeferredCommand, World } from '../world/types';
import {
    popSubscriptionSilence,
    pushSubscriptionSilence,
    setBeforeStructuralMutation,
    setDeferredPeekers,
    setDeferredScopeRunner,
} from './hooks';

type ValueSlot = {
    base: unknown;
    override: unknown;
    hasOverride: boolean;
};

type EntityState = {
    alive: boolean;
    spawned: boolean;
    nullified: boolean;
    traits: Map<Trait, ValueSlot>;
    rels: Map<Relation, Map<Entity, ValueSlot>>;
};

type Snap = {
    traits: Map<Trait, unknown>;
    pairs: Map<Relation, Map<Entity, unknown>>;
};

const viewCache = new WeakMap<World, { version: number; states: Map<Entity, EntityState> | null }>();

function bump(world: World) {
    world[$internal].deferredVersion++;
}

function isCommittedAlive(world: World, entity: Entity): boolean {
    const ctx = world[$internal];
    if (ctx.deferredGhosts.has(entity)) return false;
    return isEntityAlive(ctx.entityIndex, entity);
}

function enqueue(world: World, command: DeferredCommand) {
    const ctx = world[$internal];
    ctx.deferredStack[ctx.deferredStack.length - 1]!.push(command);
    ctx.deferredPending++;
    bump(world);
}

function slotValue(trait: Trait, slot: ValueSlot): unknown {
    if (!slot.hasOverride) return slot.base;
    const type = trait[$internal].type;
    if (type === 'tag') return undefined;
    if (type === 'aos') return slot.override;
    const defaults =
        slot.base && typeof slot.base === 'object'
            ? (slot.base as Record<string, unknown>)
            : (getSchemaDefaults(trait.schema, type) ?? {});
    return { ...defaults, ...(slot.override as Record<string, unknown>) };
}

function freshState(world: World, entity: Entity): EntityState {
    const alive = isCommittedAlive(world, entity);
    const state: EntityState = {
        alive,
        spawned: false,
        nullified: false,
        traits: new Map(),
        rels: new Map(),
    };
    if (!alive) return state;

    const traitSet = world[$internal].entityTraits.get(entity);
    if (traitSet) {
        for (const trait of traitSet) {
            if (trait[$internal].relation) continue;
            state.traits.set(trait, {
                base: getTrait(world, entity, trait),
                override: undefined,
                hasOverride: false,
            });
        }
    }

    for (const relation of world[$internal].relations) {
        const targets = getRelationTargets(world, relation, entity);
        if (targets.length === 0) continue;
        const pairs = new Map<Entity, ValueSlot>();
        const trait = relation[$internal].trait;
        for (const target of targets) {
            pairs.set(target, {
                base:
                    trait[$internal].type === 'tag'
                        ? undefined
                        : getRelationData(world, entity, relation, target),
                override: undefined,
                hasOverride: false,
            });
        }
        state.rels.set(relation, pairs);
    }

    return state;
}

function ensure(world: World, states: Map<Entity, EntityState>, entity: Entity): EntityState {
    const existing = states.get(entity);
    if (existing) return existing;

    const state = freshState(world, entity);
    for (const pairs of state.rels.values()) {
        for (const target of pairs.keys()) {
            const targetState = states.get(target);
            if (targetState && !targetState.alive) pairs.delete(target);
        }
    }
    states.set(entity, state);
    return state;
}

function unlink(states: Map<Entity, EntityState>, entity: Entity) {
    for (const state of states.values()) {
        for (const pairs of state.rels.values()) pairs.delete(entity);
    }
}

function relationsOf(world: World, states: Map<Entity, EntityState>): Relation[] {
    const set = new Set<Relation>(world[$internal].relations);
    for (const state of states.values()) {
        for (const relation of state.rels.keys()) set.add(relation);
    }
    return [...set];
}

function sourcesPointingTo(
    world: World,
    states: Map<Entity, EntityState>,
    relation: Relation,
    target: Entity
): Entity[] {
    const sources: Entity[] = [];
    const seen = new Set<Entity>();

    for (const [entity, state] of states) {
        seen.add(entity);
        if (!state.alive) continue;
        if (state.rels.get(relation)?.has(target)) sources.push(entity);
    }

    if (world[$internal].relations.has(relation)) {
        for (const source of getEntitiesWithRelationTo(world, relation, target)) {
            if (seen.has(source) || world[$internal].deferredGhosts.has(source)) continue;
            if (!isCommittedAlive(world, source)) continue;
            sources.push(source);
        }
    }

    return sources;
}

function targetsOf(
    world: World,
    states: Map<Entity, EntityState>,
    relation: Relation,
    entity: Entity
): Entity[] {
    const state = states.get(entity);
    if (state) return [...(state.rels.get(relation)?.keys() ?? [])];
    if (!world[$internal].relations.has(relation)) return [];
    return [...getRelationTargets(world, relation, entity)];
}

function kill(world: World, states: Map<Entity, EntityState>, entity: Entity) {
    const queue: Entity[] = [entity];
    const processed = new Set<Entity>();

    while (queue.length > 0) {
        const current = queue.pop()!;
        if (processed.has(current)) continue;

        const state = ensure(world, states, current);
        if (!state.alive) {
            processed.add(current);
            continue;
        }

        processed.add(current);

        if (state.spawned) {
            state.alive = false;
            state.nullified = true;
            state.traits.clear();
            state.rels.clear();
            unlink(states, current);
            continue;
        }

        const doomed: Entity[] = [];
        for (const relation of relationsOf(world, states)) {
            const autoDestroy = relation[$internal].autoDestroy;
            if (autoDestroy === 'source') {
                doomed.push(...sourcesPointingTo(world, states, relation, current));
            } else if (autoDestroy === 'target') {
                doomed.push(...targetsOf(world, states, relation, current));
            }
        }

        state.alive = false;
        state.traits.clear();
        state.rels.clear();
        unlink(states, current);

        for (const next of doomed) queue.push(next);
    }
}

function targetIsAlive(states: Map<Entity, EntityState>, target: Entity): boolean {
    const state = states.get(target);
    return state ? state.alive : true;
}

function applyPair(
    states: Map<Entity, EntityState>,
    state: EntityState,
    pair: RelationPair,
    exclusiveOverride = false
) {
    const { relation, target, params } = pair[$internal];
    if (typeof target !== 'number') return;
    if (!targetIsAlive(states, target)) return;

    let pairs = state.rels.get(relation);
    if (!pairs) {
        pairs = new Map();
        state.rels.set(relation, pairs);
    }

    if (exclusiveOverride || relation[$internal].exclusive) pairs.clear();

    const trait = relation[$internal].trait;
    const existing = pairs.get(target);
    if (!existing) {
        pairs.set(target, {
            base:
                trait[$internal].type === 'tag'
                    ? undefined
                    : (getSchemaDefaults(trait.schema, trait[$internal].type) ?? undefined),
            override: params,
            hasOverride: params !== undefined,
        });
    } else if (params !== undefined) {
        existing.override = params;
        existing.hasOverride = true;
    }
}

function applyConfigurable(
    states: Map<Entity, EntityState>,
    state: EntityState,
    config: ConfigurableTrait
) {
    if (isRelationPair(config)) {
        applyPair(states, state, config);
        return;
    }

    let trait: Trait;
    let params: unknown;
    if (Array.isArray(config)) {
        [trait, params] = config as [Trait, unknown];
    } else {
        trait = config as Trait;
        params = undefined;
    }

    if (trait[$internal].relation) return;

    const existing = state.traits.get(trait);
    if (!existing) {
        state.traits.set(trait, {
            base:
                trait[$internal].type === 'tag'
                    ? undefined
                    : (getSchemaDefaults(trait.schema, trait[$internal].type) ?? undefined),
            override: params,
            hasOverride: params !== undefined,
        });
    } else if (params !== undefined) {
        existing.override = params;
        existing.hasOverride = true;
    }
}

function applyRemove(state: EntityState, config: Trait | RelationPair) {
    if (isRelationPair(config)) {
        const { relation, target } = config[$internal];
        if (target === '*') {
            state.rels.set(relation, new Map());
            return;
        }
        if (typeof target === 'number') state.rels.get(relation)?.delete(target);
        return;
    }

    if (config[$internal].relation) {
        state.rels.set(config[$internal].relation, new Map());
        return;
    }

    state.traits.delete(config);
}

function applyAddExclusive(states: Map<Entity, EntityState>, state: EntityState, pair: RelationPair) {
    const { relation, target, params } = pair[$internal];
    if (target === '*') {
        state.rels.set(relation, new Map());
        return;
    }
    if (typeof target !== 'number' || !targetIsAlive(states, target)) {
        state.rels.set(relation, new Map());
        return;
    }

    const trait = relation[$internal].trait;
    const existing = state.rels.get(relation)?.get(target);
    const pairs = new Map<Entity, ValueSlot>();
    if (existing && params === undefined) pairs.set(target, existing);
    else {
        pairs.set(target, {
            base:
                trait[$internal].type === 'tag'
                    ? undefined
                    : (getSchemaDefaults(trait.schema, trait[$internal].type) ?? undefined),
            override: params,
            hasOverride: params !== undefined,
        });
    }
    state.rels.set(relation, pairs);
}

function executableCommands(world: World, commands: DeferredCommand[]): DeferredCommand[] {
    const worldEntity = world[$internal].worldEntity;
    const index = commands.findIndex(
        (command) => command.type === 'destroy' && command.entity === worldEntity
    );
    if (index === -1) return commands;
    return commands.slice(0, index);
}

function throwsOnWorldEntity(world: World, commands: DeferredCommand[]): boolean {
    const worldEntity = world[$internal].worldEntity;
    return commands.some((command) => command.type === 'destroy' && command.entity === worldEntity);
}

function simulate(
    world: World,
    commands: DeferredCommand[],
    states = new Map<Entity, EntityState>()
) {
    for (const state of states.values()) state.spawned = false;

    for (const command of commands) {
        if (command.type === 'destroy' && command.entity === world[$internal].worldEntity) break;

        if (command.type === 'spawn') {
            const state = ensure(world, states, command.entity);
            state.alive = true;
            state.spawned = true;
            state.nullified = false;
            state.traits.clear();
            state.rels.clear();
            for (const trait of command.traits) applyConfigurable(states, state, trait);
            continue;
        }

        const state = ensure(world, states, command.entity);
        if (!state.alive) continue;

        if (command.type === 'destroy') kill(world, states, command.entity);
        else if (command.type === 'add') {
            for (const trait of command.traits) applyConfigurable(states, state, trait);
        } else if (command.type === 'remove') {
            for (const trait of command.traits) applyRemove(state, trait);
        } else applyAddExclusive(states, state, command.pair);
    }

    const nullified = new Set<Entity>();
    for (const [entity, state] of states) {
        if (state.nullified) nullified.add(entity);
    }
    return { states, nullified };
}

function foldedStates(world: World): Map<Entity, EntityState> | null {
    const ctx = world[$internal];
    if (ctx.deferredPending === 0) return null;

    const cached = viewCache.get(world);
    if (cached && cached.version === ctx.deferredVersion) return cached.states;

    const states = new Map<Entity, EntityState>();
    for (const buffer of ctx.deferredStack) {
        if (buffer.length === 0) {
            for (const state of states.values()) state.spawned = false;
            continue;
        }
        simulate(world, executableCommands(world, buffer), states);
    }

    viewCache.set(world, { version: ctx.deferredVersion, states });
    return states;
}

function releaseGhost(world: World, entity: Entity) {
    const ctx = world[$internal];
    if (!ctx.deferredGhosts.has(entity)) return;
    ctx.deferredGhosts.delete(entity);
    ctx.entityTraits.delete(entity);
    const eid = getEntityId(entity);
    for (let i = 0; i < ctx.entityMasks.length; i++) {
        if (ctx.entityMasks[i]) ctx.entityMasks[i]![eid] = 0;
    }
    releaseEntity(ctx.entityIndex, entity);
}

function traitsTouching(traits: ConfigurableTrait[], nullified: Set<Entity>): ConfigurableTrait[] {
    return traits.filter((trait) => {
        if (!isRelationPair(trait)) return true;
        const target = trait[$internal].target;
        return typeof target !== 'number' || !nullified.has(target);
    });
}

function replay(world: World, commands: DeferredCommand[], nullified: Set<Entity>) {
    for (const entity of nullified) releaseGhost(world, entity);

    for (const command of commands) {
        if (nullified.has(command.entity)) continue;

        if (command.type === 'spawn') {
            const traits = traitsTouching(command.traits, nullified);
            world[$internal].deferredGhosts.delete(command.entity);
            initEntity(world, command.entity, traits);
            continue;
        }

        if (!isCommittedAlive(world, command.entity)) continue;

        if (command.type === 'destroy') {
            destroyEntity(world, command.entity);
            continue;
        }

        if (command.type === 'add') {
            const traits = traitsTouching(command.traits, nullified);
            if (traits.length > 0) addTrait(world, command.entity, ...traits);
            continue;
        }

        if (command.type === 'remove') {
            removeTrait(world, command.entity, ...command.traits);
            continue;
        }

        const { relation, target } = command.pair[$internal];
        if (target === '*') {
            removeTrait(world, command.entity, command.pair);
            continue;
        }
        if (typeof target !== 'number' || nullified.has(target)) {
            const existing = getRelationTargets(world, relation, command.entity);
            for (const current of existing) {
                removeTrait(world, command.entity, relation(current));
            }
            continue;
        }

        const existing = getRelationTargets(world, relation, command.entity);
        for (const current of existing) {
            if (current !== target) removeTrait(world, command.entity, relation(current));
        }
        addTrait(world, command.entity, command.pair);
    }
}

function applySimulatedValues(world: World, states: Map<Entity, EntityState>) {
    for (const [entity, state] of states) {
        if (!state.alive || state.nullified || !isCommittedAlive(world, entity)) continue;

        for (const [trait, slot] of state.traits) {
            if (!slot.hasOverride || !hasTrait(world, entity, trait)) continue;
            const value = slotValue(trait, slot);
            if (value === undefined) continue;
            setTrait(world, entity, trait, value, false);
        }

        for (const [relation, pairs] of state.rels) {
            const trait = relation[$internal].trait;
            if (trait[$internal].type === 'tag') continue;
            for (const [target, slot] of pairs) {
                if (!slot.hasOverride) continue;
                const value = slotValue(trait, slot);
                if (!value || typeof value !== 'object') continue;
                setRelationData(world, entity, relation, target, value as Record<string, unknown>);
            }
        }
    }
}

function snapshotEntity(world: World, entity: Entity): Snap {
    const snap: Snap = { traits: new Map(), pairs: new Map() };
    const traitSet = world[$internal].entityTraits.get(entity);
    if (traitSet) {
        for (const trait of traitSet) {
            if (trait[$internal].relation) continue;
            snap.traits.set(trait, getTrait(world, entity, trait));
        }
    }
    for (const relation of world[$internal].relations) {
        const targets = getRelationTargets(world, relation, entity);
        if (targets.length === 0) continue;
        const pairs = new Map<Entity, unknown>();
        for (const target of targets) {
            pairs.set(target, getRelationData(world, entity, relation, target));
        }
        snap.pairs.set(relation, pairs);
    }
    return snap;
}

function snapshotWorld(world: World): Map<Entity, Snap> {
    const snaps = new Map<Entity, Snap>();
    const ctx = world[$internal];
    const aliveCount = ctx.entityIndex.aliveCount;
    for (let i = 0; i < aliveCount; i++) {
        const entity = ctx.entityIndex.dense[i]!;
        if (ctx.deferredGhosts.has(entity)) continue;
        snaps.set(entity, snapshotEntity(world, entity));
    }
    return snaps;
}

function fire(world: World, kind: 'add' | 'remove', trait: Trait, entity: Entity, target?: Entity) {
    const data = getTraitInstance(world[$internal].traitInstances, trait);
    if (!data) return;
    const subs = kind === 'add' ? data.addSubscriptions : data.removeSubscriptions;
    for (const sub of subs) sub(entity, target);
}

function aliveNow(world: World): Set<Entity> {
    const alive = new Set<Entity>();
    const ctx = world[$internal];
    for (let i = 0; i < ctx.entityIndex.aliveCount; i++) {
        const entity = ctx.entityIndex.dense[i]!;
        if (!ctx.deferredGhosts.has(entity)) alive.add(entity);
    }
    return alive;
}

function diffEntities(world: World, before: Map<Entity, Snap>) {
    const afterAlive = aliveNow(world);

    for (const [entity, snap] of before) {
        if (!afterAlive.has(entity)) {
            for (const trait of snap.traits.keys()) fire(world, 'remove', trait, entity);
            for (const [relation, pairs] of snap.pairs) {
                for (const target of pairs.keys()) {
                    fire(world, 'remove', relation[$internal].trait, entity, target);
                }
            }
            continue;
        }

        const now = snapshotEntity(world, entity);
        for (const trait of snap.traits.keys()) {
            if (!now.traits.has(trait)) fire(world, 'remove', trait, entity);
        }
        for (const [trait, value] of now.traits) {
            if (!snap.traits.has(trait)) fire(world, 'add', trait, entity);
            else if (
                !shallowEqual(snap.traits.get(trait), value) &&
                trait[$internal].type !== 'tag'
            ) {
                setChanged(world, entity, trait);
            }
        }

        const relations = new Set<Relation>([...snap.pairs.keys(), ...now.pairs.keys()]);
        for (const relation of relations) {
            const prev = snap.pairs.get(relation) ?? new Map<Entity, unknown>();
            const next = now.pairs.get(relation) ?? new Map<Entity, unknown>();
            const trait = relation[$internal].trait;
            for (const target of prev.keys()) {
                if (!next.has(target)) fire(world, 'remove', trait, entity, target);
            }
            for (const [target, value] of next) {
                if (!prev.has(target)) fire(world, 'add', trait, entity, target);
                else if (trait[$internal].type !== 'tag' && !shallowEqual(prev.get(target), value)) {
                    setPairChanged(world, entity, trait, target);
                }
            }
        }
    }

    for (const entity of afterAlive) {
        if (before.has(entity)) continue;
        const now = snapshotEntity(world, entity);
        for (const trait of now.traits.keys()) fire(world, 'add', trait, entity);
        for (const [relation, pairs] of now.pairs) {
            for (const target of pairs.keys()) {
                fire(world, 'add', relation[$internal].trait, entity, target);
            }
        }
    }
}

function effectiveMembers(query: QueryInstance): Set<number> {
    const members = new Set<number>();
    for (const entity of query.entities.dense) {
        if (!query.toRemove.has(entity)) members.add(entity);
    }
    return members;
}

function queriesOf(world: World): QueryInstance[] {
    return [...world[$internal].queriesHashMap.values()];
}

function diffQueries(world: World, before: Map<QueryInstance, Set<number>>) {
    const seen = new Set<QueryInstance>(before.keys());
    for (const query of queriesOf(world)) seen.add(query);

    for (const query of seen) {
        if (query.addSubscriptions.size === 0 && query.removeSubscriptions.size === 0) continue;
        const prev = before.get(query) ?? new Set<number>();
        const next = effectiveMembers(query);
        for (const entity of next) {
            if (!prev.has(entity)) {
                for (const sub of query.addSubscriptions) sub(entity as Entity);
            }
        }
        for (const entity of prev) {
            if (!next.has(entity)) {
                for (const sub of query.removeSubscriptions) sub(entity as Entity);
            }
        }
    }
}

function applyCommands(world: World, commands: DeferredCommand[]) {
    const run = executableCommands(world, commands);
    const shouldThrow = throwsOnWorldEntity(world, commands);
    const { states, nullified } = simulate(world, run);
    const before = snapshotWorld(world);
    const queryBefore = new Map<QueryInstance, Set<number>>();
    for (const query of queriesOf(world)) {
        if (query.addSubscriptions.size === 0 && query.removeSubscriptions.size === 0) continue;
        queryBefore.set(query, effectiveMembers(query));
    }

    pushSubscriptionSilence();
    try {
        replay(world, run, nullified);
        applySimulatedValues(world, states);
    } finally {
        popSubscriptionSilence();
    }

    diffQueries(world, queryBefore);
    diffEntities(world, before);

    if (shouldThrow) throw new Error('Koota: Cannot destroy the world entity.');
}

function drainBuffer(world: World, buffer: DeferredCommand[]) {
    const ctx = world[$internal];
    const alreadyApplying = ctx.deferredApplying;
    ctx.deferredApplying = true;
    try {
        while (buffer.length > 0) {
            const commands = buffer.splice(0, buffer.length);
            ctx.deferredPending -= commands.length;
            bump(world);
            applyCommands(world, commands);
        }
    } finally {
        ctx.deferredApplying = alreadyApplying;
        bump(world);
    }
}

function entityHasPending(world: World, entity: Entity): boolean {
    for (const buffer of world[$internal].deferredStack) {
        for (const command of buffer) {
            if (command.entity === entity) return true;
        }
    }
    return false;
}

export function discardDeferred(world: World) {
    const ctx = world[$internal];
    ctx.deferredGhosts.clear();
    ctx.deferredStack = [[]];
    ctx.deferredPending = 0;
    ctx.deferredApplying = false;
    bump(world);
}

export function createDeferred(world: World): Deferred {
    return {
        spawn(...traits) {
            const ctx = world[$internal];
            const entity = allocateEntity(ctx.entityIndex);
            ctx.entityTraits.set(entity, new Set());
            ctx.deferredGhosts.add(entity);
            enqueue(world, { type: 'spawn', entity, traits });
            return entity;
        },

        destroy(entity) {
            enqueue(world, { type: 'destroy', entity });
        },

        add(entity, ...traits) {
            enqueue(world, { type: 'add', entity, traits });
        },

        remove(entity, ...traits) {
            enqueue(world, { type: 'remove', entity, traits });
        },

        addExclusive(entity, pair) {
            if (!isRelationPair(pair)) {
                throw new Error('Koota: addExclusive expects a relation pair.');
            }
            enqueue(world, { type: 'addExclusive', entity, pair });
        },

        flush() {
            drainBuffer(
                world,
                world[$internal].deferredStack[world[$internal].deferredStack.length - 1]!
            );
        },
    };
}

setBeforeStructuralMutation((world, entity) => {
    const ctx = world[$internal];
    if (ctx.deferredApplying || ctx.deferredPending === 0) return;
    if (!entityHasPending(world, entity)) return;
    const stack = ctx.deferredStack;
    for (let i = 0; i < stack.length; i++) drainBuffer(world, stack[i]!);
});

setDeferredScopeRunner((world, fn) => {
    const buffer: DeferredCommand[] = [];
    world[$internal].deferredStack.push(buffer);
    try {
        return fn();
    } finally {
        try {
            drainBuffer(world, buffer);
        } finally {
            const stack = world[$internal].deferredStack;
            const index = stack.lastIndexOf(buffer);
            if (index !== -1) stack.splice(index, 1);
        }
    }
});

setDeferredPeekers({
    has(world, entity, trait) {
        const states = foldedStates(world);
        if (!states) return undefined;
        const state = states.get(entity);
        if (!state) return undefined;
        if (!state.alive) return false;

        if (isRelationPair(trait)) {
            const { relation, target } = trait[$internal];
            const pairs = state.rels.get(relation);
            if (target === '*') return !!pairs && pairs.size > 0;
            if (typeof target !== 'number') return false;
            return !!pairs?.has(target);
        }

        if (trait[$internal].relation) {
            const pairs = state.rels.get(trait[$internal].relation);
            return !!pairs && pairs.size > 0;
        }

        return state.traits.has(trait);
    },

    get(world, entity, trait) {
        const states = foldedStates(world);
        if (!states) return undefined;
        const state = states.get(entity);
        if (!state) return undefined;
        if (!state.alive) return { value: undefined };

        if (isRelationPair(trait)) {
            const { relation, target } = trait[$internal];
            if (typeof target !== 'number') return { value: undefined };
            const slot = state.rels.get(relation)?.get(target);
            if (!slot) return { value: undefined };
            return { value: slotValue(relation[$internal].trait, slot) };
        }

        if (trait[$internal].relation) return { value: undefined };
        const slot = state.traits.get(trait);
        if (!slot) return { value: undefined };
        return { value: slotValue(trait, slot) };
    },

    targets(world, entity, relation) {
        const states = foldedStates(world);
        if (!states) return undefined;
        const state = states.get(entity);
        if (!state) return undefined;
        if (!state.alive) return [];
        return [...(state.rels.get(relation)?.keys() ?? [])];
    },
});

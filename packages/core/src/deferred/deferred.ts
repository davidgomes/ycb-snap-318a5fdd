import { $internal } from '../common';
import { destroyEntity, initAllocatedEntity } from '../entity/entity';
import type { Entity } from '../entity/types';
import { allocateEntity, isEntityAlive, releaseEntity } from '../entity/utils/entity-index';
import { getEntityId } from '../entity/utils/pack-entity';
import type { QueryInstance } from '../query/types';
import {
    getEntitiesWithRelationTo,
    getRelationData,
    getRelationTargets,
    hasRelationToTarget,
} from '../relation/relation';
import type { Relation, RelationPair } from '../relation/types';
import { isRelationPair } from '../relation/utils/is-relation';
import { getSchemaDefaults } from '../storage';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import { hasTraitInstance } from '../trait/trait-instance';
import type { ConfigurableTrait, Trait, TraitInstance } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world';
import { popSubscriptionSuppress, pushSubscriptionSuppress } from './flags';

type SpawnCommand = { type: 'spawn'; entity: Entity; traits: ConfigurableTrait[] };
type DestroyCommand = { type: 'destroy'; entity: Entity };
type AddCommand = { type: 'add'; entity: Entity; traits: ConfigurableTrait[] };
type RemoveCommand = { type: 'remove'; entity: Entity; traits: (Trait | RelationPair)[] };
type AddExclusiveCommand = { type: 'addExclusive'; entity: Entity; pair: RelationPair };

type Command = SpawnCommand | DestroyCommand | AddCommand | RemoveCommand | AddExclusiveCommand;

type Flat =
    | { type: 'spawn'; entity: Entity; traits: ConfigurableTrait[] }
    | { type: 'destroy'; entity: Entity }
    | { type: 'add'; entity: Entity; config: ConfigurableTrait }
    | { type: 'remove'; entity: Entity; config: Trait | RelationPair }
    | { type: 'addExclusive'; entity: Entity; pair: RelationPair };

type Buffer = {
    commands: Command[];
    entities: Set<Entity>;
};

type Loc = { index: number; traitIndex: number };

type Backend = {
    alive(entity: Entity): boolean;
    isWorldEntity(entity: Entity): boolean;
    isStaged(entity: Entity): boolean;
    onNullified(entity: Entity): void;
    spawn(entity: Entity, traits: ConfigurableTrait[]): void;
    destroy(entity: Entity): void;
    add(entity: Entity, config: ConfigurableTrait): void;
    remove(entity: Entity, config: Trait | RelationPair): void;
    addExclusive(entity: Entity, pair: RelationPair): void;
};

const WORLD_ENTITY_ERROR = 'Koota: Cannot destroy the world entity.';
const previews = new WeakMap<World, { version: number; sim: Sim }>();

function createBuffer(): Buffer {
    return { commands: [], entities: new Set() };
}

export function installDeferred(world: World): void {
    const ctx = world[$internal];
    ctx.deferredStack = [createBuffer()];
    ctx.deferredPending = 0;
    ctx.deferredFlushing = false;
    ctx.deferredVersion = 0;
    ctx.stagedSpawns = new Set();
    previews.delete(world);
}

export function resetDeferred(world: World): void {
    const ctx = world[$internal];
    const staged = [...ctx.stagedSpawns];
    for (const entity of staged) releaseShell(world, entity);
    ctx.deferredStack = [createBuffer()];
    ctx.deferredPending = 0;
    ctx.deferredFlushing = false;
    ctx.deferredVersion++;
    ctx.stagedSpawns.clear();
    previews.delete(world);
}

export function createDeferred(world: World) {
    return {
        spawn(...traits: ConfigurableTrait[]): Entity {
            const entity = allocateStaged(world);
            enqueue(world, { type: 'spawn', entity, traits: traits.slice() });
            return entity;
        },

        destroy(entity: Entity): void {
            enqueue(world, { type: 'destroy', entity });
        },

        add(entity: Entity, ...traits: ConfigurableTrait[]): void {
            if (traits.length === 0) return;
            enqueue(world, { type: 'add', entity, traits: traits.slice() });
        },

        remove(entity: Entity, ...traits: (Trait | RelationPair)[]): void {
            if (traits.length === 0) return;
            enqueue(world, { type: 'remove', entity, traits: traits.slice() });
        },

        addExclusive(entity: Entity, pair: RelationPair): void {
            if (!isRelationPair(pair)) {
                throw new Error('Koota: addExclusive expects a relation pair.');
            }
            enqueue(world, { type: 'addExclusive', entity, pair });
        },

        flush(): void {
            const ctx = world[$internal];
            const stack = ctx.deferredStack;
            const current = stack[stack.length - 1];
            const later: Command[][] = [];
            for (let i = stack.length - 2; i >= 0; i--) later.push(stack[i].commands);
            runAndDiff(world, [current.commands], later);
        },
    };
}

function allocateStaged(world: World): Entity {
    const ctx = world[$internal];
    const entity = allocateEntity(ctx.entityIndex);
    ctx.stagedSpawns.add(entity);
    return entity;
}

function enqueue(world: World, command: Command): void {
    const ctx = world[$internal];
    const buffer = ctx.deferredStack[ctx.deferredStack.length - 1];
    buffer.commands.push(command);
    buffer.entities.add(command.entity);
    ctx.deferredPending++;
    ctx.deferredVersion++;
    previews.delete(world);
}

export function shouldReadDeferred(world: World): boolean {
    const ctx = world[$internal];
    return ctx.deferredPending > 0 && !ctx.deferredFlushing;
}

export function enterDeferredScope(world: World): void {
    world[$internal].deferredStack.push(createBuffer());
}

export function exitDeferredScope(world: World): void {
    const ctx = world[$internal];
    const stack = ctx.deferredStack;
    if (stack.length <= 1) return;
    const buffer = stack.pop()!;
    const later: Command[][] = [];
    for (let i = stack.length - 1; i >= 0; i--) later.push(stack[i].commands);
    runAndDiff(world, [buffer.commands], later);
}

export function flushIfPending(world: World, entity: Entity): void {
    const ctx = world[$internal];
    if (ctx.deferredFlushing || ctx.deferredPending === 0) return;

    let found = false;
    for (const buffer of ctx.deferredStack) {
        if (buffer.entities.has(entity)) {
            found = true;
            break;
        }
    }
    if (!found) return;

    const lists: Command[][] = [];
    for (let i = ctx.deferredStack.length - 1; i >= 0; i--) {
        lists.push(ctx.deferredStack[i].commands);
    }
    runAndDiff(world, lists, []);
}

function runAndDiff(world: World, executeLists: Command[][], laterLists: Command[][]): void {
    if (world[$internal].deferredFlushing) return;
    if (executeLists.every((list) => list.length === 0)) return;

    const before = captureSnapshot(world);
    const ctx = world[$internal];
    ctx.deferredFlushing = true;
    ctx.deferredVersion++;
    previews.delete(world);
    pushSubscriptionSuppress();

    let error: unknown;
    try {
        drive(executeLists, laterLists, createWorldBackend(world), true);
    } catch (caught) {
        error = caught;
    } finally {
        popSubscriptionSuppress();
        ctx.deferredFlushing = false;
        recount(world);
    }

    emitDiff(world, before, captureSnapshot(world));
    if (error) throw error;
}

function recount(world: World): void {
    const ctx = world[$internal];
    let pending = 0;
    for (const buffer of ctx.deferredStack) {
        buffer.entities.clear();
        for (const command of buffer.commands) {
            pending++;
            buffer.entities.add(command.entity);
        }
    }
    ctx.deferredPending = pending;
    ctx.deferredVersion++;
    previews.delete(world);
}

function releaseShell(world: World, entity: Entity): void {
    const ctx = world[$internal];
    ctx.stagedSpawns.delete(entity);
    if (!isEntityAlive(ctx.entityIndex, entity)) return;
    if (ctx.entityTraits.has(entity)) return;

    const eid = getEntityId(entity);
    for (let i = 0; i < ctx.entityMasks.length; i++) {
        if (ctx.entityMasks[i]) ctx.entityMasks[i][eid] = 0;
    }
    releaseEntity(ctx.entityIndex, entity);
}

function createWorldBackend(world: World): Backend {
    const ctx = world[$internal];
    return {
        alive(entity) {
            if (ctx.stagedSpawns.has(entity)) return false;
            return isEntityAlive(ctx.entityIndex, entity);
        },
        isWorldEntity(entity) {
            return entity === ctx.worldEntity;
        },
        isStaged(entity) {
            return ctx.stagedSpawns.has(entity);
        },
        onNullified(entity) {
            releaseShell(world, entity);
        },
        spawn(entity, traits) {
            if (!isEntityAlive(ctx.entityIndex, entity)) {
                ctx.stagedSpawns.delete(entity);
                return;
            }
            ctx.stagedSpawns.delete(entity);
            initAllocatedEntity(world, entity, traits);
        },
        destroy(entity) {
            if (!isEntityAlive(ctx.entityIndex, entity)) return;
            destroyEntity(world, entity);
        },
        add(entity, config) {
            applyDeferredAdd(world, entity, config);
        },
        remove(entity, config) {
            if (!isEntityAlive(ctx.entityIndex, entity)) return;
            removeTrait(world, entity, config);
        },
        addExclusive(entity, pair) {
            applyAddExclusive(world, entity, pair);
        },
    };
}

function applyDeferredAdd(world: World, entity: Entity, config: ConfigurableTrait): void {
    if (!isEntityAlive(world[$internal].entityIndex, entity)) return;
    if (world[$internal].stagedSpawns.has(entity)) return;

    if (isRelationPair(config)) {
        const pairCtx = config[$internal];
        const target = pairCtx.target;
        if (typeof target !== 'number') return;
        if (hasRelationToTarget(world, pairCtx.relation, entity, target)) return;
        addTrait(world, entity, config);
        return;
    }

    const trait = (Array.isArray(config) ? config[0] : config) as Trait;
    if (hasTrait(world, entity, trait)) return;
    addTrait(world, entity, config);
}

function applyAddExclusive(world: World, entity: Entity, pair: RelationPair): void {
    if (!isEntityAlive(world[$internal].entityIndex, entity)) return;
    if (world[$internal].stagedSpawns.has(entity)) return;

    const pairCtx = pair[$internal];
    const relation = pairCtx.relation;
    const target = pairCtx.target;
    const relationTrait = relation[$internal].trait;

    if (target === '*') {
        if (hasTrait(world, entity, relationTrait)) removeTrait(world, entity, pair);
        return;
    }

    if (typeof target !== 'number') return;

    const targets = getRelationTargets(world, relation, entity);
    for (const current of targets) {
        if (current !== target) removeTrait(world, entity, relation(current));
    }

    if (!hasRelationToTarget(world, relation, entity, target)) {
        addTrait(world, entity, pair);
    } else if (pairCtx.params !== undefined) {
        setTrait(world, entity, pair, pairCtx.params, true);
    }
}

function drive(
    executeLists: Command[][],
    laterLists: Command[][],
    backend: Backend,
    throwOnWorldEntity: boolean
): void {
    const lists = executeLists.concat(laterLists);
    const limit = executeLists.length;

    for (let i = 0; i < limit; i++) {
        const commands = lists[i].splice(0, lists[i].length);
        const { flat, nullified } = optimize(commands);
        for (const entity of nullified) backend.onNullified(entity);

        for (const command of flat) {
            if (command.type === 'destroy' && backend.isWorldEntity(command.entity)) {
                if (throwOnWorldEntity) throw new Error(WORLD_ENTITY_ERROR);
                break;
            }

            if (command.type === 'destroy' && !backend.alive(command.entity)) {
                if (backend.isStaged(command.entity)) {
                    stripSpawn(lists, i + 1, command.entity);
                    backend.onNullified(command.entity);
                }
                continue;
            }

            if (command.type !== 'spawn' && !backend.alive(command.entity)) {
                if (!pullSpawn(lists, i + 1, command.entity, backend)) continue;
            }

            applyFlat(backend, command);
        }
    }
}

function applyFlat(backend: Backend, command: Flat): void {
    switch (command.type) {
        case 'spawn':
            backend.spawn(command.entity, command.traits);
            break;
        case 'destroy':
            backend.destroy(command.entity);
            break;
        case 'add':
            backend.add(command.entity, command.config);
            break;
        case 'remove':
            backend.remove(command.entity, command.config);
            break;
        case 'addExclusive':
            backend.addExclusive(command.entity, command.pair);
            break;
    }
}

function stripSpawn(lists: Command[][], start: number, entity: Entity): void {
    for (let i = start; i < lists.length; i++) {
        const list = lists[i];
        for (let k = list.length - 1; k >= 0; k--) {
            if (list[k].type === 'spawn' && list[k].entity === entity) list.splice(k, 1);
        }
    }
}

function pullSpawn(lists: Command[][], start: number, entity: Entity, backend: Backend): boolean {
    for (let i = start; i < lists.length; i++) {
        const list = lists[i];
        let spawnIndex = -1;
        let destroyed = false;
        for (let k = 0; k < list.length; k++) {
            const command = list[k];
            if (command.entity !== entity) continue;
            if (command.type === 'spawn') spawnIndex = k;
            if (command.type === 'destroy') destroyed = true;
        }
        if (spawnIndex >= 0 && destroyed) return false;
        if (spawnIndex >= 0) {
            const spawn = list.splice(spawnIndex, 1)[0] as SpawnCommand;
            backend.spawn(spawn.entity, spawn.traits);
            return backend.alive(spawn.entity);
        }
    }
    return false;
}

function configIdentity(config: ConfigurableTrait | Trait | RelationPair): string {
    if (isRelationPair(config)) {
        const pair = config[$internal];
        return `p:${pair.relation[$internal].trait.id}:${String(pair.target)}`;
    }
    const trait = (Array.isArray(config) ? config[0] : config) as Trait;
    return `t:${trait.id}`;
}

function hasParams(config: ConfigurableTrait): boolean {
    if (isRelationPair(config)) return config[$internal].params !== undefined;
    return Array.isArray(config);
}

function relationTraitId(config: ConfigurableTrait | Trait | RelationPair): number | undefined {
    if (isRelationPair(config)) return config[$internal].relation[$internal].trait.id;
    const trait = (Array.isArray(config) ? config[0] : config) as Trait;
    return trait[$internal].relation ? trait.id : undefined;
}

function optimize(commands: Command[]): { flat: Flat[]; nullified: Set<Entity> } {
    const flatIn: Flat[] = [];
    for (const command of commands) {
        if (command.type === 'add') {
            for (const config of command.traits) {
                flatIn.push({ type: 'add', entity: command.entity, config });
            }
        } else if (command.type === 'remove') {
            for (const config of command.traits) {
                flatIn.push({ type: 'remove', entity: command.entity, config });
            }
        } else if (command.type === 'spawn') {
            flatIn.push({ type: 'spawn', entity: command.entity, traits: command.traits.slice() });
        } else {
            flatIn.push(command);
        }
    }

    const spawned = new Set<Entity>();
    const destroyed = new Set<Entity>();
    for (const command of flatIn) {
        if (command.type === 'spawn') spawned.add(command.entity);
        if (command.type === 'destroy') destroyed.add(command.entity);
    }
    const nullified = new Set<Entity>();
    for (const entity of spawned) {
        if (destroyed.has(entity)) nullified.add(entity);
    }

    const out: Flat[] = [];
    const locs = new Map<string, Loc>();
    const dead = new Set<Entity>(nullified);

    const remember = (entity: Entity, config: ConfigurableTrait, index: number, traitIndex: number) => {
        locs.set(`${entity}:${configIdentity(config)}`, { index, traitIndex });
    };

    const forgetRelation = (entity: Entity, relationId: number) => {
        const prefix = `${entity}:p:${relationId}:`;
        for (const key of locs.keys()) {
            if (key.startsWith(prefix)) locs.delete(key);
        }
        locs.delete(`${entity}:x:${relationId}`);
    };

    for (const command of flatIn) {
        if (nullified.has(command.entity)) continue;
        if (command.type !== 'spawn' && dead.has(command.entity)) continue;

        if (command.type === 'destroy') {
            dead.add(command.entity);
            out.push(command);
            continue;
        }

        if (command.type === 'spawn') {
            const traits: ConfigurableTrait[] = [];
            const local = new Map<string, number>();
            for (const config of command.traits) {
                const id = configIdentity(config);
                const previous = local.get(id);
                if (previous !== undefined) {
                    if (hasParams(config)) traits[previous] = config;
                    continue;
                }
                local.set(id, traits.length);
                traits.push(config);
            }
            const index = out.length;
            out.push({ type: 'spawn', entity: command.entity, traits });
            for (let i = 0; i < traits.length; i++) remember(command.entity, traits[i], index, i);
            continue;
        }

        if (command.type === 'add') {
            const key = `${command.entity}:${configIdentity(command.config)}`;
            const previous = locs.get(key);
            if (previous) {
                if (hasParams(command.config)) {
                    const target = out[previous.index];
                    if (target.type === 'spawn') target.traits[previous.traitIndex] = command.config;
                    else if (target.type === 'add') {
                        out[previous.index] = {
                            type: 'add',
                            entity: command.entity,
                            config: command.config,
                        };
                    }
                }
                continue;
            }
            const index = out.length;
            out.push(command);
            locs.set(key, { index, traitIndex: -1 });
            continue;
        }

        if (command.type === 'remove') {
            locs.delete(`${command.entity}:${configIdentity(command.config)}`);
            const relationId = relationTraitId(command.config);
            if (
                isRelationPair(command.config) &&
                command.config[$internal].target === '*' &&
                relationId !== undefined
            ) {
                forgetRelation(command.entity, relationId);
            }
            out.push(command);
            continue;
        }

        const relationId = command.pair[$internal].relation[$internal].trait.id;
        forgetRelation(command.entity, relationId);
        const key = `${command.entity}:x:${relationId}`;
        const previous = locs.get(key);
        if (previous && out[previous.index]?.type === 'addExclusive') {
            out[previous.index] = command;
            continue;
        }
        const index = out.length;
        out.push(command);
        locs.set(key, { index, traitIndex: -1 });
    }

    return { flat: out, nullified };
}

type PairSnap = Map<Entity, Map<Entity, unknown>>;

type TraitSnap = {
    instance: TraitInstance;
    isRelation: boolean;
    trackChange: boolean;
    entities: Set<Entity>;
    values: Map<Entity, unknown>;
    pairs: PairSnap;
};

type Snapshot = {
    traits: Map<Trait, TraitSnap>;
    queries: Map<QueryInstance, Set<Entity>>;
};

function cloneValue(value: unknown): unknown {
    if (Array.isArray(value)) return value.slice();
    if (value && typeof value === 'object') return { ...(value as Record<string, unknown>) };
    return value;
}

function captureSnapshot(world: World): Snapshot {
    const ctx = world[$internal];
    const traits = new Map<Trait, TraitSnap>();

    for (const instance of ctx.traitInstances) {
        if (!instance) continue;
        const trackChange = instance.changeSubscriptions.size > 0;
        if (
            instance.addSubscriptions.size === 0 &&
            instance.removeSubscriptions.size === 0 &&
            !trackChange
        ) {
            continue;
        }
        traits.set(instance.trait, {
            instance,
            isRelation: Boolean(instance.trait[$internal].relation),
            trackChange,
            entities: new Set(),
            values: new Map(),
            pairs: new Map(),
        });
    }

    for (const [rawEntity, entityTraits] of ctx.entityTraits) {
        const entity = rawEntity as Entity;
        if (!isEntityAlive(ctx.entityIndex, entity)) continue;
        for (const trait of entityTraits) {
            const snap = traits.get(trait);
            if (!snap) continue;
            if (snap.isRelation) {
                const relation = trait[$internal].relation!;
                const targets = getRelationTargets(world, relation, entity);
                const pairs = new Map<Entity, unknown>();
                for (const target of targets) {
                    pairs.set(
                        target,
                        snap.trackChange
                            ? cloneValue(getRelationData(world, entity, relation, target))
                            : undefined
                    );
                }
                snap.pairs.set(entity, pairs);
            } else {
                snap.entities.add(entity);
                if (snap.trackChange) snap.values.set(entity, cloneValue(getTrait(world, entity, trait)));
            }
        }
    }

    const queries = new Map<QueryInstance, Set<Entity>>();
    const seen = new Set<QueryInstance>();
    const collect = (query: QueryInstance | undefined) => {
        if (!query || seen.has(query)) return;
        seen.add(query);
        if (query.addSubscriptions.size === 0 && query.removeSubscriptions.size === 0) return;
        const members = new Set<Entity>();
        const dense = query.entities.dense;
        for (let i = 0; i < dense.length; i++) {
            const entity = dense[i] as Entity;
            if (!query.toRemove.has(entity)) members.add(entity);
        }
        queries.set(query, members);
    };
    for (const query of ctx.queriesHashMap.values()) collect(query);
    for (const query of ctx.queryInstances) collect(query);

    return { traits, queries };
}

function emitDiff(world: World, before: Snapshot, after: Snapshot): void {
    const traitKeys = new Set<Trait>([...before.traits.keys(), ...after.traits.keys()]);

    const removes: Array<{
        subs: Set<(entity: Entity, target?: Entity) => void>;
        entity: Entity;
        target?: Entity;
    }> = [];
    const adds: typeof removes = [];
    const changes: typeof removes = [];

    for (const trait of traitKeys) {
        const previous = before.traits.get(trait);
        const next = after.traits.get(trait);
        const instance = (next ?? previous)!.instance;
        const isRelation = (next ?? previous)!.isRelation;

        if (isRelation) {
            const beforePairs = previous?.pairs ?? new Map();
            const afterPairs = next?.pairs ?? new Map();
            const entities = new Set<Entity>([...beforePairs.keys(), ...afterPairs.keys()]);
            for (const entity of entities) {
                const beforeTargets = beforePairs.get(entity) ?? new Map();
                const afterTargets = afterPairs.get(entity) ?? new Map();
                for (const [target, value] of beforeTargets) {
                    if (!afterTargets.has(target)) {
                        removes.push({ subs: instance.removeSubscriptions, entity, target });
                    } else if (
                        (next ?? previous)!.trackChange &&
                        !shallowEqual(value, afterTargets.get(target))
                    ) {
                        changes.push({ subs: instance.changeSubscriptions, entity, target });
                    }
                }
                for (const target of afterTargets.keys()) {
                    if (!beforeTargets.has(target)) {
                        adds.push({ subs: instance.addSubscriptions, entity, target });
                    }
                }
            }
        } else {
            const beforeEntities = previous?.entities ?? new Set<Entity>();
            const afterEntities = next?.entities ?? new Set<Entity>();
            for (const entity of beforeEntities) {
                if (!afterEntities.has(entity)) {
                    removes.push({ subs: instance.removeSubscriptions, entity });
                } else if (
                    (next ?? previous)!.trackChange &&
                    !shallowEqual(previous?.values.get(entity), next?.values.get(entity))
                ) {
                    changes.push({ subs: instance.changeSubscriptions, entity });
                }
            }
            for (const entity of afterEntities) {
                if (!beforeEntities.has(entity)) {
                    adds.push({ subs: instance.addSubscriptions, entity });
                }
            }
        }
    }

    const fire = (
        events: Array<{
            subs: Set<(entity: Entity, target?: Entity) => void>;
            entity: Entity;
            target?: Entity;
        }>
    ) => {
        events.sort((a, b) => a.entity - b.entity || (a.target ?? 0) - (b.target ?? 0));
        for (const event of events) {
            const subs = Array.from(event.subs);
            for (const sub of subs) sub(event.entity, event.target);
        }
    };

    fire(removes);
    fire(adds);
    fire(changes);

    const queryKeys = new Set<QueryInstance>([...before.queries.keys(), ...after.queries.keys()]);
    const queryRemoves: Array<{ query: QueryInstance; entity: Entity }> = [];
    const queryAdds: Array<{ query: QueryInstance; entity: Entity }> = [];
    for (const query of queryKeys) {
        const previous = before.queries.get(query) ?? new Set<Entity>();
        const next = after.queries.get(query) ?? membersOf(query);
        for (const entity of previous) {
            if (!next.has(entity)) queryRemoves.push({ query, entity });
        }
        for (const entity of next) {
            if (!previous.has(entity)) queryAdds.push({ query, entity });
        }
    }
    queryRemoves.sort((a, b) => a.entity - b.entity);
    queryAdds.sort((a, b) => a.entity - b.entity);
    for (const event of queryRemoves) {
        const subs = Array.from(event.query.removeSubscriptions);
        for (const sub of subs) sub(event.entity);
    }
    for (const event of queryAdds) {
        const subs = Array.from(event.query.addSubscriptions);
        for (const sub of subs) sub(event.entity);
    }
}

function membersOf(query: QueryInstance): Set<Entity> {
    const members = new Set<Entity>();
    const dense = query.entities.dense;
    for (let i = 0; i < dense.length; i++) {
        const entity = dense[i] as Entity;
        if (!query.toRemove.has(entity)) members.add(entity);
    }
    return members;
}

type PairRec = { present: boolean; value: unknown };
type EntRec = {
    alive: boolean;
    traits: Map<Trait, { present: boolean; value: unknown }>;
    pairs: Map<Relation<Trait>, Map<Entity, PairRec>>;
};

class Sim {
    ents = new Map<Entity, EntRec>();
    voided = new Set<Entity>();
    spawned = new Set<Entity>();
    initialStaged: Set<Entity>;

    constructor(private world: World) {
        this.initialStaged = new Set(world[$internal].stagedSpawns);
    }

    worldAlive(entity: Entity): boolean {
        if (this.initialStaged.has(entity) && !this.spawned.has(entity)) return false;
        return isEntityAlive(this.world[$internal].entityIndex, entity);
    }

    rec(entity: Entity): EntRec {
        const existing = this.ents.get(entity);
        if (existing) return existing;
        const alive = this.worldAlive(entity);
        const record: EntRec = { alive, traits: new Map(), pairs: new Map() };
        this.ents.set(entity, record);
        if (alive) this.copy(entity, record);
        return record;
    }

    copy(entity: Entity, record: EntRec): void {
        const traits = this.world[$internal].entityTraits.get(entity);
        if (!traits) return;
        for (const trait of traits) {
            const relation = trait[$internal].relation;
            if (relation) {
                const pairs = new Map<Entity, PairRec>();
                for (const target of getRelationTargets(this.world, relation, entity)) {
                    pairs.set(target, {
                        present: true,
                        value: getRelationData(this.world, entity, relation, target),
                    });
                }
                record.pairs.set(relation, pairs);
                record.traits.set(trait, { present: pairs.size > 0, value: undefined });
            } else {
                record.traits.set(trait, { present: true, value: getTrait(this.world, entity, trait) });
            }
        }
    }

    alive(entity: Entity): boolean {
        const record = this.ents.get(entity);
        if (record) return record.alive;
        if (this.voided.has(entity)) return false;
        return this.worldAlive(entity);
    }

    isStaged(entity: Entity): boolean {
        return this.initialStaged.has(entity) && !this.spawned.has(entity) && !this.voided.has(entity);
    }
}

function resolveRelationValue(relation: Relation<Trait>, previous: unknown, params: unknown): unknown {
    const trait = relation[$internal].trait;
    if (trait[$internal].type === 'tag') return {};
    return resolveTraitValue(trait, previous, params);
}

function resolveTraitValue(trait: Trait, previous: unknown, params: unknown): unknown {
    const type = trait[$internal].type;
    if (type === 'tag') return undefined;
    if (type === 'aos') {
        if (params !== undefined) return params;
        if (previous !== undefined) return previous;
        const schema = trait.schema as unknown;
        return typeof schema === 'function' ? (schema as () => unknown)() : undefined;
    }
    const defaults = getSchemaDefaults(trait.schema as Record<string, unknown>, type) ?? {};
    const prev =
        previous && typeof previous === 'object' ? (previous as Record<string, unknown>) : {};
    const next = params && typeof params === 'object' ? (params as Record<string, unknown>) : {};
    return { ...defaults, ...prev, ...next };
}

function createSimBackend(world: World, sim: Sim): Backend {
    const relationsOf = () => {
        const relations = new Set<Relation<Trait>>(world[$internal].relations);
        for (const record of sim.ents.values()) {
            for (const relation of record.pairs.keys()) relations.add(relation);
        }
        return relations;
    };

    const removePair = (source: Entity, relation: Relation<Trait>, target: Entity) => {
        const record = sim.rec(source);
        const pairs = record.pairs.get(relation);
        const pair = pairs?.get(target);
        if (pair) pair.present = false;
    };

    const targetsOf = (relation: Relation<Trait>, entity: Entity): Entity[] => {
        const pairs = sim.rec(entity).pairs.get(relation);
        if (!pairs) return [];
        const targets: Entity[] = [];
        for (const [target, pair] of pairs) {
            if (pair.present) targets.push(target);
        }
        return targets;
    };

    const sourcesOf = (relation: Relation<Trait>, target: Entity): Entity[] => {
        const result: Entity[] = [];
        const seen = new Set<Entity>();
        const trait = relation[$internal].trait;
        if (hasTraitInstance(world[$internal].traitInstances, trait)) {
            const real = getEntitiesWithRelationTo(world, relation, target);
            for (const source of real) {
                if (sim.ents.has(source)) continue;
                if (!sim.alive(source) || seen.has(source)) continue;
                seen.add(source);
                result.push(source);
            }
        }
        for (const [entity, record] of sim.ents) {
            if (!record.alive || seen.has(entity)) continue;
            if (record.pairs.get(relation)?.get(target)?.present) {
                seen.add(entity);
                result.push(entity);
            }
        }
        return result;
    };

    return {
        alive: (entity) => sim.alive(entity),
        isWorldEntity: (entity) => entity === world[$internal].worldEntity,
        isStaged: (entity) => sim.isStaged(entity),
        onNullified(entity) {
            const record = sim.rec(entity);
            record.alive = false;
            sim.voided.add(entity);
        },
        spawn(entity, traits) {
            if (sim.voided.has(entity)) return;
            let record = sim.ents.get(entity);
            if (!record) {
                record = { alive: true, traits: new Map(), pairs: new Map() };
                sim.ents.set(entity, record);
            }
            record.alive = true;
            sim.spawned.add(entity);
            sim.voided.delete(entity);
            for (const config of traits) this.add(entity, config);
        },
        destroy(entity) {
            const queue: Entity[] = [entity];
            const processed = new Set<Entity>();
            while (queue.length > 0) {
                const current = queue.pop()!;
                if (processed.has(current) || !sim.alive(current)) continue;
                processed.add(current);
                sim.rec(current);
                for (const relation of relationsOf()) {
                    for (const source of sourcesOf(relation, current)) {
                        if (!sim.alive(source)) continue;
                        removePair(source, relation, current);
                        if (relation[$internal].autoDestroy === 'source') queue.push(source);
                    }
                    if (relation[$internal].autoDestroy === 'target') {
                        for (const target of targetsOf(relation, current)) {
                            if (sim.alive(target) && !processed.has(target)) queue.push(target);
                        }
                    }
                }
                sim.rec(current).alive = false;
            }
        },
        add(entity, config) {
            const record = sim.rec(entity);
            if (!record.alive) return;
            if (isRelationPair(config)) {
                const pair = config[$internal];
                if (typeof pair.target !== 'number') return;
                let pairs = record.pairs.get(pair.relation);
                if (!pairs) {
                    pairs = new Map();
                    record.pairs.set(pair.relation, pairs);
                }
                const existing = pairs.get(pair.target);
                if (existing?.present) return;
                if (pair.relation[$internal].exclusive) {
                    for (const current of pairs.values()) current.present = false;
                }
                pairs.set(pair.target, {
                    present: true,
                    value: resolveRelationValue(pair.relation, undefined, pair.params),
                });
                record.traits.set(pair.relation[$internal].trait, { present: true, value: undefined });
                return;
            }

            const trait = (Array.isArray(config) ? config[0] : config) as Trait;
            const params = Array.isArray(config) ? config[1] : undefined;
            const current = record.traits.get(trait);
            if (current?.present) return;
            record.traits.set(trait, {
                present: true,
                value: resolveTraitValue(trait, undefined, params),
            });
        },
        remove(entity, config) {
            const record = sim.rec(entity);
            if (!record.alive) return;
            if (isRelationPair(config)) {
                const pair = config[$internal];
                const pairs = record.pairs.get(pair.relation);
                if (!pairs) return;
                if (pair.target === '*') {
                    for (const current of pairs.values()) current.present = false;
                } else if (typeof pair.target === 'number') {
                    const current = pairs.get(pair.target);
                    if (current) current.present = false;
                }
                let any = false;
                for (const current of pairs.values()) {
                    if (current.present) any = true;
                }
                record.traits.set(pair.relation[$internal].trait, { present: any, value: undefined });
                return;
            }
            const trait = config as Trait;
            const relation = trait[$internal].relation;
            if (relation) {
                const pairs = record.pairs.get(relation);
                if (pairs) {
                    for (const current of pairs.values()) current.present = false;
                }
            }
            const current = record.traits.get(trait);
            if (current) current.present = false;
            else record.traits.set(trait, { present: false, value: undefined });
        },
        addExclusive(entity, pair) {
            const record = sim.rec(entity);
            if (!record.alive) return;
            const pairCtx = pair[$internal];
            let pairs = record.pairs.get(pairCtx.relation);
            if (!pairs) {
                pairs = new Map();
                record.pairs.set(pairCtx.relation, pairs);
            }
            if (pairCtx.target === '*') {
                for (const current of pairs.values()) current.present = false;
                record.traits.set(pairCtx.relation[$internal].trait, { present: false, value: undefined });
                return;
            }
            if (typeof pairCtx.target !== 'number') return;
            for (const [target, current] of pairs) {
                if (target !== pairCtx.target) current.present = false;
            }
            const existing = pairs.get(pairCtx.target);
            if (existing?.present) {
                if (pairCtx.params !== undefined) {
                    existing.value = resolveRelationValue(pairCtx.relation, existing.value, pairCtx.params);
                }
            } else {
                pairs.set(pairCtx.target, {
                    present: true,
                    value: resolveRelationValue(pairCtx.relation, undefined, pairCtx.params),
                });
            }
            record.traits.set(pairCtx.relation[$internal].trait, { present: true, value: undefined });
        },
    };
}


function getSim(world: World): Sim {
    const ctx = world[$internal];
    const cached = previews.get(world);
    if (cached && cached.version === ctx.deferredVersion) return cached.sim;

    const sim = new Sim(world);
    const lists: Command[][] = [];
    for (let i = ctx.deferredStack.length - 1; i >= 0; i--) {
        lists.push(ctx.deferredStack[i].commands.slice());
    }
    drive(lists, [], createSimBackend(world, sim), false);
    previews.set(world, { version: ctx.deferredVersion, sim });
    return sim;
}

export function deferredAlive(world: World, entity: Entity): boolean {
    return getSim(world).alive(entity);
}

export function deferredHas(world: World, entity: Entity, trait: Trait | RelationPair): boolean {
    const sim = getSim(world);
    if (!sim.alive(entity)) return false;
    const record = sim.rec(entity);
    if (isRelationPair(trait)) {
        const pair = trait[$internal];
        const pairs = record.pairs.get(pair.relation);
        if (!pairs) return false;
        if (pair.target === '*') {
            for (const current of pairs.values()) {
                if (current.present) return true;
            }
            return false;
        }
        if (typeof pair.target !== 'number') return false;
        return pairs.get(pair.target)?.present ?? false;
    }
    const relation = trait[$internal].relation;
    if (relation) {
        const pairs = record.pairs.get(relation);
        if (!pairs) return false;
        for (const current of pairs.values()) {
            if (current.present) return true;
        }
        return false;
    }
    return record.traits.get(trait)?.present ?? false;
}

export function deferredGet(world: World, entity: Entity, trait: Trait | RelationPair): unknown {
    const sim = getSim(world);
    if (!sim.alive(entity)) return undefined;
    const record = sim.rec(entity);
    if (isRelationPair(trait)) {
        const pair = trait[$internal];
        if (typeof pair.target !== 'number') return undefined;
        const current = record.pairs.get(pair.relation)?.get(pair.target);
        if (!current?.present) return undefined;
        return current.value;
    }
    const current = record.traits.get(trait);
    if (!current?.present) return undefined;
    return current.value;
}

export function deferredTargets(world: World, entity: Entity, relation: Relation<Trait>): Entity[] {
    const sim = getSim(world);
    if (!sim.alive(entity)) return [];
    const pairs = sim.rec(entity).pairs.get(relation);
    if (!pairs) return [];
    const targets: Entity[] = [];
    for (const [target, pair] of pairs) {
        if (pair.present) targets.push(target);
    }
    return targets;
}

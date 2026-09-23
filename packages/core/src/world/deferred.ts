import { $internal } from '../common';
import { destroyEntity, initEntity } from '../entity/entity';
import type { Entity } from '../entity/types';
import { allocateEntity, isEntityAlive, releaseEntity } from '../entity/utils/entity-index';
import { getRelationTargets, hasRelationPair } from '../relation/relation';
import type { Relation, RelationPair } from '../relation/types';
import { isRelationPair } from '../relation/utils/is-relation';
import { getSchemaDefaults } from '../storage';
import {
    addTrait,
    getTrait,
    hasTrait,
    registerTrait,
    removeTrait,
    setTrait,
} from '../trait/trait';
import { getTraitInstance } from '../trait/trait-instance';
import type { ConfigurableTrait, Trait } from '../trait/types';
import type { World } from './types';

type AddOp = { kind: 'add'; config: ConfigurableTrait; params: any };
type RemoveOp = { kind: 'remove'; target: Trait | RelationPair };
type Op = AddOp | RemoveOp;

type EntityCommands = {
    spawned: boolean;
    destroyed: boolean;
    cleared: Set<Relation<Trait>>;
    ops: Map<unknown, Op>;
};

export type DeferredBuffer = Map<Entity, EntityCommands>;

export type Deferred = {
    spawn(...traits: ConfigurableTrait[]): Entity;
    destroy(entity: Entity): void;
    add(entity: Entity, ...traits: ConfigurableTrait[]): void;
    remove(entity: Entity, ...traits: (Trait | RelationPair)[]): void;
    addExclusive(entity: Entity, pair: RelationPair): void;
    flush(): void;
};

const pairKeys = new WeakMap<Relation<Trait>, Map<number, object>>();

function getPairKey(relation: Relation<Trait>, target: Entity): object {
    let map = pairKeys.get(relation);
    if (!map) pairKeys.set(relation, (map = new Map()));
    let key = map.get(target);
    if (!key) map.set(target, (key = {}));
    return key;
}

function getKey(target: ConfigurableTrait | RelationPair): unknown {
    if (isRelationPair(target)) {
        const { relation, target: t } = target[$internal];
        return getPairKey(relation as Relation<Trait>, t as Entity);
    }
    return Array.isArray(target) ? target[0] : target;
}

function getStack(world: World): DeferredBuffer[] {
    const ctx = world[$internal];
    if (ctx.deferredStack.length === 0) ctx.deferredStack.push(new Map());
    return ctx.deferredStack;
}

function getCommands(world: World, entity: Entity): EntityCommands {
    const stack = getStack(world);
    const buffer = stack[stack.length - 1];
    let cmds = buffer.get(entity);
    if (!cmds) {
        cmds = { spawned: false, destroyed: false, cleared: new Set(), ops: new Map() };
        buffer.set(entity, cmds);
    }
    return cmds;
}

function setOp(cmds: EntityCommands, key: unknown, op: Op) {
    // Re-insert so the latest command keeps its place in execution order.
    cmds.ops.delete(key);
    cmds.ops.set(key, op);
}

function clearRelation(cmds: EntityCommands, relation: Relation<Trait>) {
    const map = pairKeys.get(relation);
    if (map) for (const key of map.values()) cmds.ops.delete(key);
    cmds.cleared.add(relation);
}

function deferAdd(world: World, entity: Entity, config: ConfigurableTrait) {
    const cmds = getCommands(world, entity);
    if (cmds.destroyed) return;
    if (isRelationPair(config)) {
        const { relation, target, params } = config[$internal];
        if (typeof target !== 'number') return;
        if (relation[$internal].exclusive) clearRelation(cmds, relation as Relation<Trait>);
        setOp(cmds, getKey(config), { kind: 'add', config, params });
        return;
    }
    const params = Array.isArray(config) ? config[1] : undefined;
    setOp(cmds, getKey(config), { kind: 'add', config, params });
}

function deferRemove(world: World, entity: Entity, target: Trait | RelationPair) {
    const cmds = getCommands(world, entity);
    if (cmds.destroyed) return;
    if (isRelationPair(target)) {
        const { relation, target: t } = target[$internal];
        if (t === '*') {
            clearRelation(cmds, relation as Relation<Trait>);
            return;
        }
    } else if (target[$internal].relation) {
        clearRelation(cmds, target[$internal].relation);
        return;
    }
    setOp(cmds, getKey(target), { kind: 'remove', target });
}

function deferDestroy(world: World, entity: Entity) {
    const stack = getStack(world);
    const buffer = stack[stack.length - 1];
    const cmds = getCommands(world, entity);
    if (cmds.spawned) {
        // Spawn and destroy within the same buffer cancel each other out.
        buffer.delete(entity);
        releaseEntity(world[$internal].entityIndex, entity);
        return;
    }
    cmds.destroyed = true;
    cmds.ops.clear();
    cmds.cleared.clear();
}

function executeCommands(world: World, entity: Entity, cmds: EntityCommands) {
    const ctx = world[$internal];
    if (!isEntityAlive(ctx.entityIndex, entity)) return;

    if (cmds.destroyed) {
        if (entity === ctx.worldEntity) {
            throw new Error('Koota: The world entity cannot be destroyed.');
        }
        destroyEntity(world, entity);
        return;
    }

    if (cmds.spawned) initEntity(world, entity);

    for (const relation of cmds.cleared) {
        const targets = getRelationTargets(world, relation, entity).slice();
        for (const target of targets) {
            const op = cmds.ops.get(getPairKey(relation, target));
            if (op?.kind === 'add') continue;
            removeTrait(world, entity, relation(target));
        }
    }

    for (const op of cmds.ops.values()) {
        if (!isEntityAlive(ctx.entityIndex, entity)) return;
        if (op.kind === 'remove') {
            removeTrait(world, entity, op.target);
            continue;
        }
        const config = op.config;
        const present = isRelationPair(config)
            ? hasRelationPair(world, entity, config)
            : hasTrait(world, entity, Array.isArray(config) ? config[0] : (config as Trait));
        if (!present) addTrait(world, entity, config);
        else if (op.params !== undefined) {
            const target = isRelationPair(config)
                ? config
                : Array.isArray(config)
                  ? config[0]
                  : (config as Trait);
            setTrait(world, entity, target, op.params);
        }
    }
}

export function flushBuffer(world: World, buffer: DeferredBuffer) {
    // Commands issued during execution land in a fresh buffer.
    const stack = world[$internal].deferredStack;
    const index = stack.indexOf(buffer);
    if (index !== -1) stack[index] = new Map();
    for (const [entity, cmds] of buffer) executeCommands(world, entity, cmds);
}

export function flushEntity(world: World, entity: Entity) {
    const stack = world[$internal].deferredStack;
    for (let i = 0; i < stack.length; i++) {
        const cmds = stack[i].get(entity);
        if (!cmds) continue;
        stack[i].delete(entity);
        executeCommands(world, entity, cmds);
    }
}

export function hasPendingCommands(world: World, entity: Entity): boolean {
    const stack = world[$internal].deferredStack;
    for (let i = 0; i < stack.length; i++) if (stack[i].has(entity)) return true;
    return false;
}

export function pushDeferredScope(world: World) {
    getStack(world).push(new Map());
}

export function popDeferredScope(world: World) {
    const buffer = world[$internal].deferredStack.pop();
    if (buffer) flushBuffer(world, buffer);
}

type Lookup = { found: false } | { found: true; op: Op | null };

function lookup(world: World, entity: Entity, key: unknown, relation?: Relation<Trait>): Lookup {
    const stack = world[$internal].deferredStack;
    for (let i = stack.length - 1; i >= 0; i--) {
        const cmds = stack[i].get(entity);
        if (!cmds) continue;
        if (cmds.destroyed) return { found: true, op: null };
        const op = cmds.ops.get(key);
        if (op) return { found: true, op };
        if (relation && cmds.cleared.has(relation)) return { found: true, op: null };
        if (cmds.spawned) return { found: true, op: null };
    }
    return { found: false };
}

function pairRelation(target: Trait | RelationPair): Relation<Trait> | undefined {
    if (isRelationPair(target)) return target[$internal].relation as Relation<Trait>;
    return target[$internal].relation ?? undefined;
}

export function deferredHas(world: World, entity: Entity, target: Trait | RelationPair): boolean {
    const result = lookup(world, entity, getKey(target), pairRelation(target));
    if (!result.found) {
        return isRelationPair(target)
            ? hasRelationPair(world, entity, target)
            : hasTrait(world, entity, target);
    }
    return result.op?.kind === 'add';
}

export function deferredGet(world: World, entity: Entity, target: Trait | RelationPair) {
    const result = lookup(world, entity, getKey(target), pairRelation(target));
    const current = () =>
        isEntityAlive(world[$internal].entityIndex, entity)
            ? getTrait(world, entity, target)
            : undefined;
    if (!result.found) return current();
    const op = result.op;
    if (!op || op.kind !== 'add') return undefined;

    const trait = isRelationPair(target) ? target[$internal].relation[$internal].trait : target;
    const traitCtx = trait[$internal];
    const existing = deferredStateExists(world, entity, target) ? current() : undefined;
    if (traitCtx.type === 'aos') return op.params ?? existing ?? defaultsFor(world, trait);
    const base = existing ?? defaultsFor(world, trait);
    if (base === undefined && op.params === undefined) return undefined;
    return { ...base, ...op.params };
}

function deferredStateExists(world: World, entity: Entity, target: Trait | RelationPair) {
    if (!isEntityAlive(world[$internal].entityIndex, entity)) return false;
    if (hasPendingSpawn(world, entity)) return false;
    return isRelationPair(target)
        ? hasRelationPair(world, entity, target)
        : hasTrait(world, entity, target);
}

function hasPendingSpawn(world: World, entity: Entity) {
    const stack = world[$internal].deferredStack;
    for (let i = 0; i < stack.length; i++) if (stack[i].get(entity)?.spawned) return true;
    return false;
}

function defaultsFor(world: World, trait: Trait) {
    const ctx = world[$internal];
    if (!getTraitInstance(ctx.traitInstances, trait)) registerTrait(world, trait);
    const instance = getTraitInstance(ctx.traitInstances, trait)!;
    return getSchemaDefaults(instance.schema, trait[$internal].type);
}

export function createDeferred(world: World): Deferred {
    return {
        spawn(...traits: ConfigurableTrait[]) {
            const entity = allocateEntity(world[$internal].entityIndex);
            const cmds = getCommands(world, entity);
            cmds.spawned = true;
            for (const config of traits) deferAdd(world, entity, config);
            return entity;
        },
        destroy(entity: Entity) {
            deferDestroy(world, entity);
        },
        add(entity: Entity, ...traits: ConfigurableTrait[]) {
            for (const config of traits) deferAdd(world, entity, config);
        },
        remove(entity: Entity, ...traits: (Trait | RelationPair)[]) {
            for (const target of traits) deferRemove(world, entity, target);
        },
        addExclusive(entity: Entity, pair: RelationPair) {
            const cmds = getCommands(world, entity);
            if (cmds.destroyed) return;
            clearRelation(cmds, pair[$internal].relation as Relation<Trait>);
            if (pair[$internal].target === '*') return;
            setOp(cmds, getKey(pair), { kind: 'add', config: pair, params: pair[$internal].params });
        },
        flush() {
            const stack = getStack(world);
            flushBuffer(world, stack[stack.length - 1]);
        },
    };
}

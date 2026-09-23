import { $internal } from '../common';
import { destroyEntity, initEntity } from '../entity/entity';
import type { Entity } from '../entity/types';
import { allocateEntity, isEntityAlive } from '../entity/utils/entity-index';
import { getOrderedTraitRelation, isOrderedTrait } from '../relation/ordered';
import { OrderedList } from '../relation/ordered-list';
import {
    getEntitiesWithRelationTo,
    getRelationData,
    getRelationTargets,
    hasRelationToTarget,
} from '../relation/relation';
import type { Relation, RelationPair } from '../relation/types';
import { isRelationPair } from '../relation/utils/is-relation';
import { getSchemaDefaults } from '../storage';
import { addTrait, getTrait, hasTrait, registerTrait, removeTrait, setTrait } from '../trait/trait';
import { hasTraitInstance } from '../trait/trait-instance';
import type { ConfigurableTrait, Trait } from '../trait/types';
import type { World } from '../world';
import type {
    AddCommand,
    AddPairCommand,
    Command,
    CommandBuffer,
    DeferredCommands,
    EntityRecord,
    RelationRecord,
} from './types';

const ABSENT = Symbol('absent');
const bufferPool: CommandBuffer[] = [];

export function acquireCommandBuffer(): CommandBuffer {
    return (
        bufferPool.pop() ?? {
            commands: [],
            records: new Map(),
            pairAddsByTarget: new Map(),
        }
    );
}

export function releaseCommandBuffer(buffer: CommandBuffer) {
    buffer.commands.length = 0;
    buffer.records.clear();
    buffer.pairAddsByTarget.clear();
    bufferPool.push(buffer);
}

export function createDeferred(world: World): DeferredCommands {
    return {
        spawn: (...traits) => deferSpawn(world, traits),
        destroy: (entity) => deferDestroy(world, entity),
        add: (entity, ...traits) => deferAdd(world, entity, traits),
        remove: (entity, ...traits) => deferRemove(world, entity, traits),
        addExclusive: (entity, pair) => deferAddExclusive(world, entity, pair),
        flush: () => flushScope(world, world[$internal].deferredScopes.length - 1),
    };
}

/**
 * Opens a scope whose deferred commands are executed when the scope is closed.
 */
export function pushDeferredScope(world: World) {
    world[$internal].deferredScopes.push(acquireCommandBuffer());
}

export function popDeferredScope(world: World) {
    const scopes = world[$internal].deferredScopes;
    try {
        flushScope(world, scopes.length - 1);
    } finally {
        releaseCommandBuffer(scopes.pop()!);
    }
}

export function resetDeferredScopes(world: World) {
    const scopes = world[$internal].deferredScopes;
    for (let i = 0; i < scopes.length; i++) {
        releaseCommandBuffer(scopes[i]);
        scopes[i] = acquireCommandBuffer();
    }
}

export function hasPendingCommands(world: World): boolean {
    const scopes = world[$internal].deferredScopes;
    for (let i = 0; i < scopes.length; i++) {
        if (scopes[i].commands.length > 0) return true;
    }
    return false;
}

/**
 * Executes every buffer with commands involving the entity, innermost first.
 */
export function flushPendingCommandsFor(world: World, entity: Entity) {
    const scopes = world[$internal].deferredScopes;
    for (let i = scopes.length - 1; i >= 0; i--) {
        const buffer = scopes[i];
        if (
            buffer.commands.length > 0 &&
            (buffer.records.has(entity) || buffer.pairAddsByTarget.has(entity))
        ) {
            flushScope(world, i);
        }
    }
}

function flushScope(world: World, index: number) {
    const scopes = world[$internal].deferredScopes;
    // Commands deferred while executing land in the fresh buffer and run in the next pass.
    while (scopes[index].commands.length > 0) {
        const buffer = scopes[index];
        scopes[index] = acquireCommandBuffer();
        executeBuffer(world, buffer);
    }
}

/* Enqueueing */

function currentBuffer(world: World): CommandBuffer {
    const scopes = world[$internal].deferredScopes;
    return scopes[scopes.length - 1];
}

function pushCommand<C extends Command>(buffer: CommandBuffer, command: C): C {
    buffer.commands.push(command);
    return command;
}

function killCommand(buffer: CommandBuffer, command: Command) {
    command.alive = false;
    if (command.type !== 'addPair') return;
    const incoming = buffer.pairAddsByTarget.get(command.target);
    if (!incoming) return;
    incoming.delete(command);
    if (incoming.size === 0) buffer.pairAddsByTarget.delete(command.target);
}

function getRecord(buffer: CommandBuffer, entity: Entity): EntityRecord {
    let record = buffer.records.get(entity);
    if (!record) {
        record = { spawn: null, destroyed: false, traits: new Map(), relations: new Map() };
        buffer.records.set(entity, record);
    }
    return record;
}

function getRelationRecord(record: EntityRecord, relation: Relation<Trait>): RelationRecord {
    let rel = record.relations.get(relation);
    if (!rel) {
        rel = { clear: null, pairs: new Map() };
        record.relations.set(relation, rel);
    }
    return rel;
}

function deferSpawn(world: World, traits: ConfigurableTrait[]): Entity {
    const buffer = currentBuffer(world);
    const entity = allocateEntity(world[$internal].entityIndex);
    getRecord(buffer, entity).spawn = pushCommand(buffer, { type: 'spawn', entity, alive: true });
    enqueueConfigs(world, buffer, entity, traits);
    return entity;
}

function deferAdd(world: World, entity: Entity, traits: ConfigurableTrait[]) {
    if (!isPendingAlive(world, entity)) return;
    enqueueConfigs(world, currentBuffer(world), entity, traits);
}

function deferRemove(world: World, entity: Entity, traits: (Trait | RelationPair)[]) {
    if (!isPendingAlive(world, entity)) return;
    const buffer = currentBuffer(world);

    for (const trait of traits) {
        if (isRelationPair(trait)) {
            const { relation, target } = trait[$internal];
            if (target === '*') enqueueClear(buffer, entity, relation, null);
            else if (typeof target === 'number') enqueueRemovePair(buffer, entity, relation, target);
            continue;
        }

        const relation = trait[$internal].relation;
        if (relation) {
            enqueueClear(buffer, entity, relation, null);
            continue;
        }

        const record = getRecord(buffer, entity);
        const prev = record.traits.get(trait);
        if (prev) killCommand(buffer, prev);
        record.traits.set(trait, pushCommand(buffer, { type: 'remove', entity, alive: true, trait }));
    }
}

function deferAddExclusive(world: World, entity: Entity, pair: RelationPair) {
    if (!isPendingAlive(world, entity)) return;
    const buffer = currentBuffer(world);

    if (!isRelationPair(pair)) {
        enqueueConfigs(world, buffer, entity, [pair]);
        return;
    }

    const { relation, target, params } = pair[$internal];
    if (target === '*') enqueueClear(buffer, entity, relation, null);
    else enqueueAddPair(world, buffer, entity, relation, target, params, true);
}

function deferDestroy(world: World, entity: Entity) {
    const ctx = world[$internal];
    const buffer = currentBuffer(world);

    const queue = [entity];
    while (queue.length > 0) {
        const current = queue.pop()!;

        // Execution throws, so the world entity is left out of the pending state.
        if (current === ctx.worldEntity) {
            pushCommand(buffer, { type: 'destroy', entity: current, alive: true });
            continue;
        }

        if (!isPendingAlive(world, current)) continue;

        // Resolve cascades against the pending state so entities spawned in this buffer are nullified too.
        for (const relation of ctx.relations) {
            const { autoDestroy } = relation[$internal];
            if (autoDestroy === 'source') queue.push(...previewSources(world, relation, current));
            else if (autoDestroy === 'target') queue.push(...previewTargets(world, current, relation));
        }

        markDestroyed(buffer, current);
    }
}

function markDestroyed(buffer: CommandBuffer, entity: Entity) {
    const record = getRecord(buffer, entity);

    for (const command of record.traits.values()) killCommand(buffer, command);
    record.traits.clear();

    for (const rel of record.relations.values()) {
        if (rel.clear) killCommand(buffer, rel.clear);
        for (const command of rel.pairs.values()) killCommand(buffer, command);
    }
    record.relations.clear();

    const incoming = buffer.pairAddsByTarget.get(entity);
    if (incoming) {
        for (const command of incoming) {
            command.alive = false;
            const rel = buffer.records.get(command.entity)?.relations.get(command.relation);
            if (rel?.pairs.get(entity) === command) rel.pairs.delete(entity);
        }
        buffer.pairAddsByTarget.delete(entity);
    }

    if (record.spawn) {
        record.spawn.alive = false;
        record.spawn = null;
    }

    record.destroyed = true;
    pushCommand(buffer, { type: 'destroy', entity, alive: true });
}

function enqueueConfigs(
    world: World,
    buffer: CommandBuffer,
    entity: Entity,
    configs: ConfigurableTrait[]
) {
    for (const config of configs) {
        if (isRelationPair(config)) {
            const { relation, target, params } = config[$internal];
            if (typeof target !== 'number') continue;
            enqueueAddPair(world, buffer, entity, relation, target, params, relation[$internal].exclusive);
            continue;
        }

        let trait: Trait;
        let params: unknown;
        if (Array.isArray(config)) [trait, params] = config;
        else trait = config;

        const record = getRecord(buffer, entity);
        const prev = record.traits.get(trait);
        const reset = prev !== undefined && (prev.type === 'remove' || prev.reset);
        if (prev) killCommand(buffer, prev);

        record.traits.set(
            trait,
            pushCommand(buffer, {
                type: 'add',
                entity,
                alive: true,
                trait,
                params,
                reset,
                resolved: false,
                value: undefined,
            })
        );
    }
}

function enqueueAddPair(
    world: World,
    buffer: CommandBuffer,
    entity: Entity,
    relation: Relation<Trait>,
    target: Entity,
    params: Record<string, unknown> | undefined,
    exclusive: boolean
) {
    if (!isPendingAlive(world, target)) return;

    // Registering tracks the relation so pending cascades can be resolved.
    const ctx = world[$internal];
    const relationTrait = relation[$internal].trait;
    if (!hasTraitInstance(ctx.traitInstances, relationTrait)) registerTrait(world, relationTrait);

    const rel = getRelationRecord(getRecord(buffer, entity), relation);
    const prev = rel.pairs.get(target);
    const reset = prev
        ? prev.type === 'removePair' || prev.reset
        : rel.clear !== null && rel.clear.except !== target;

    if (exclusive) enqueueClear(buffer, entity, relation, target);
    else if (prev) killCommand(buffer, prev);

    const command = pushCommand(buffer, {
        type: 'addPair',
        entity,
        alive: true,
        relation,
        target,
        params,
        reset,
        resolved: false,
        value: undefined,
    });
    rel.pairs.set(target, command);

    let incoming = buffer.pairAddsByTarget.get(target);
    if (!incoming) {
        incoming = new Set();
        buffer.pairAddsByTarget.set(target, incoming);
    }
    incoming.add(command);
}

function enqueueRemovePair(
    buffer: CommandBuffer,
    entity: Entity,
    relation: Relation<Trait>,
    target: Entity
) {
    const rel = getRelationRecord(getRecord(buffer, entity), relation);
    const prev = rel.pairs.get(target);
    if (prev) killCommand(buffer, prev);
    rel.pairs.set(
        target,
        pushCommand(buffer, { type: 'removePair', entity, alive: true, relation, target })
    );
}

function enqueueClear(
    buffer: CommandBuffer,
    entity: Entity,
    relation: Relation<Trait>,
    except: Entity | null
) {
    const rel = getRelationRecord(getRecord(buffer, entity), relation);
    if (rel.clear) killCommand(buffer, rel.clear);
    for (const command of rel.pairs.values()) killCommand(buffer, command);
    rel.pairs.clear();
    rel.clear = pushCommand(buffer, { type: 'clearPairs', entity, alive: true, relation, except });
}

/* Values */

function resolveTraitValue(world: World, command: AddCommand): unknown {
    if (command.resolved) return command.value;

    const { trait, params, entity } = command;
    const type = trait[$internal].type;
    let value: unknown;

    if (type === 'aos') {
        value = isOrderedTrait(trait)
            ? new OrderedList(world, entity, getOrderedTraitRelation(trait), trait)
            : (params ?? (trait.schema as () => unknown)());
    } else if (type === 'soa') {
        const defaults = getSchemaDefaults(trait.schema, type)!;
        const record: Record<string, unknown> = {};
        const source = params as Record<string, unknown> | undefined;
        for (const key in defaults) {
            record[key] = source && key in source ? source[key] : defaults[key];
        }
        value = record;
    }

    command.value = value;
    command.resolved = true;
    return value;
}

function resolvePairValue(command: AddPairCommand): Record<string, unknown> | undefined {
    if (command.resolved) return command.value;

    const relationTrait = command.relation[$internal].trait;
    const defaults = getSchemaDefaults(relationTrait.schema, relationTrait[$internal].type);

    command.value = defaults ? { ...defaults, ...command.params } : command.params;
    command.resolved = true;
    return command.value;
}

/** Mirrors how `getRelationData` reads a pair back from its store. */
function readPairValue(command: AddPairCommand): unknown {
    const relationTrait = command.relation[$internal].trait;
    const value = resolvePairValue(command);
    if (relationTrait[$internal].type === 'aos') return value;

    const record: Record<string, unknown> = {};
    for (const key in relationTrait.schema) record[key] = value?.[key];
    return record;
}

/* Preview */

function isPendingAlive(world: World, entity: Entity): boolean {
    const ctx = world[$internal];
    if (!isEntityAlive(ctx.entityIndex, entity)) return false;

    const scopes = ctx.deferredScopes;
    for (let i = 0; i < scopes.length; i++) {
        if (scopes[i].records.get(entity)?.destroyed) return false;
    }
    return true;
}

/**
 * Reads a trait as it will be once pending buffers flush, innermost buffer first.
 * Returns ABSENT if the entity will not have the trait.
 */
function previewTrait(world: World, entity: Entity, trait: Trait, withValue: boolean): unknown {
    if (!isPendingAlive(world, entity)) return ABSENT;

    let has = hasTrait(world, entity, trait);
    let source: AddCommand | null = null;

    const scopes = world[$internal].deferredScopes;
    for (let i = scopes.length - 1; i >= 0; i--) {
        const command = scopes[i].records.get(entity)?.traits.get(trait);
        if (!command) continue;

        if (command.type === 'remove') {
            has = false;
            source = null;
        } else {
            if (!has || command.reset) source = command;
            has = true;
        }
    }

    if (!has) return ABSENT;
    if (!withValue) return undefined;
    if (!source) return getTrait(world, entity, trait);

    const value = resolveTraitValue(world, source);
    return trait[$internal].type === 'soa' ? { ...(value as object) } : value;
}

function previewPair(
    world: World,
    entity: Entity,
    relation: Relation<Trait>,
    target: Entity,
    withValue: boolean
): unknown {
    if (!isPendingAlive(world, entity) || !isPendingAlive(world, target)) return ABSENT;

    let has = hasRelationToTarget(world, relation, entity, target);
    let source: AddPairCommand | null = null;

    const scopes = world[$internal].deferredScopes;
    for (let i = scopes.length - 1; i >= 0; i--) {
        const rel = scopes[i].records.get(entity)?.relations.get(relation);
        if (!rel) continue;

        const command = rel.pairs.get(target);
        if (command?.type === 'addPair') {
            if (!has || command.reset) source = command;
            has = true;
        } else if (command || (rel.clear && rel.clear.except !== target)) {
            has = false;
            source = null;
        }
    }

    if (!has) return ABSENT;
    if (!withValue) return undefined;
    if (!source) return getRelationData(world, entity, relation, target);
    return readPairValue(source);
}

function previewTargets(world: World, entity: Entity, relation: Relation<Trait>): Entity[] {
    if (!isPendingAlive(world, entity)) return [];

    let targets = [...getRelationTargets(world, relation, entity)];

    const scopes = world[$internal].deferredScopes;
    for (let i = scopes.length - 1; i >= 0; i--) {
        const rel = scopes[i].records.get(entity)?.relations.get(relation);
        if (!rel) continue;

        if (rel.clear) {
            const except = rel.clear.except;
            targets = targets.filter((target) => target === except);
        }

        for (const [target, command] of rel.pairs) {
            const index = targets.indexOf(target);
            if (command.type === 'addPair') {
                if (index === -1) targets.push(target);
            } else if (index !== -1) {
                targets.splice(index, 1);
            }
        }
    }

    return targets.filter((target) => isPendingAlive(world, target));
}

function previewSources(world: World, relation: Relation<Trait>, target: Entity): Entity[] {
    const sources: Entity[] = [];

    for (const source of getEntitiesWithRelationTo(world, relation, target)) {
        if (previewPair(world, source, relation, target, false) !== ABSENT) sources.push(source);
    }

    const scopes = world[$internal].deferredScopes;
    for (let i = 0; i < scopes.length; i++) {
        const incoming = scopes[i].pairAddsByTarget.get(target);
        if (!incoming) continue;

        for (const command of incoming) {
            const source = command.entity;
            if (command.relation !== relation || sources.includes(source)) continue;
            if (previewPair(world, source, relation, target, false) !== ABSENT) sources.push(source);
        }
    }

    return sources;
}

export function previewHas(world: World, entity: Entity, trait: Trait | RelationPair): boolean {
    if (isRelationPair(trait)) {
        const { relation, target } = trait[$internal];
        if (target === '*') return previewTargets(world, entity, relation).length > 0;
        return previewPair(world, entity, relation, target, false) !== ABSENT;
    }

    const relation = trait[$internal].relation;
    if (relation) return previewTargets(world, entity, relation).length > 0;

    return previewTrait(world, entity, trait, false) !== ABSENT;
}

export function previewGet(world: World, entity: Entity, trait: Trait | RelationPair): unknown {
    let value: unknown;

    if (isRelationPair(trait)) {
        const { relation, target } = trait[$internal];
        if (typeof target !== 'number') return undefined;
        value = previewPair(world, entity, relation, target, true);
    } else if (trait[$internal].relation) {
        return getTrait(world, entity, trait);
    } else {
        value = previewTrait(world, entity, trait, true);
    }

    return value === ABSENT ? undefined : value;
}

/* Execution */

function executeBuffer(world: World, buffer: CommandBuffer) {
    const ctx = world[$internal];
    let error: Error | null = null;

    try {
        const commands = buffer.commands;
        for (let i = 0; i < commands.length; i++) {
            const command = commands[i];
            if (!command.alive) continue;

            const entity = command.entity;

            if (command.type === 'destroy' && entity === ctx.worldEntity) {
                error ??= new Error('Koota: The world entity cannot be destroyed.');
                continue;
            }

            // Commands on destroyed entities are skipped.
            if (!isEntityAlive(ctx.entityIndex, entity)) continue;

            switch (command.type) {
                case 'spawn':
                    if (!ctx.entityTraits.has(entity)) initEntity(world, entity);
                    break;

                case 'destroy':
                    // A spawn nullified in the same buffer never touched queries or traits.
                    if (!ctx.entityTraits.has(entity)) ctx.entityTraits.set(entity, new Set());
                    destroyEntity(world, entity);
                    break;

                case 'add': {
                    if (!ctx.entityTraits.has(entity)) initEntity(world, entity);
                    const { trait } = command;

                    if (!hasTrait(world, entity, trait)) {
                        const value = command.resolved ? command.value : command.params;
                        addTrait(world, entity, value === undefined ? trait : [trait, value as any]);
                    } else if (command.reset && trait[$internal].type !== 'tag') {
                        setTrait(world, entity, trait, resolveTraitValue(world, command));
                    }
                    break;
                }

                case 'remove':
                    removeTrait(world, entity, command.trait);
                    break;

                case 'addPair': {
                    const { relation, target } = command;
                    if (!isEntityAlive(ctx.entityIndex, target)) break;
                    if (!ctx.entityTraits.has(entity)) initEntity(world, entity);

                    if (!hasRelationToTarget(world, relation, entity, target)) {
                        const value = command.resolved ? command.value : command.params;
                        addTrait(world, entity, relation(target, value));
                    } else if (command.reset) {
                        const value = resolvePairValue(command);
                        if (value) setTrait(world, entity, relation(target), value);
                    }
                    break;
                }

                case 'removePair':
                    if (hasRelationToTarget(world, command.relation, entity, command.target)) {
                        removeTrait(world, entity, command.relation(command.target));
                    }
                    break;

                case 'clearPairs': {
                    const { relation, except } = command;
                    const rel = buffer.records.get(entity)?.relations.get(relation);

                    for (const target of getRelationTargets(world, relation, entity)) {
                        if (target === except) continue;
                        // Pairs re-added later in the buffer stay so no remove/add events fire.
                        if (rel?.pairs.get(target)?.type === 'addPair') continue;
                        removeTrait(world, entity, relation(target));
                    }
                    break;
                }
            }
        }
    } finally {
        releaseCommandBuffer(buffer);
    }

    if (error) throw error;
}

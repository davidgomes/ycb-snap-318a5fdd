import { $internal } from '../common';
import { destroyEntity, initializeEntity } from '../entity/entity';
import type { Entity } from '../entity/types';
import { allocateEntity, isEntityAlive } from '../entity/utils/entity-index';
import { getEntityId } from '../entity/utils/pack-entity';
import { IsExcluded } from '../query/query';
import { getRelationTargets } from '../relation/relation';
import type { RelationPair } from '../relation/types';
import { isRelationPair } from '../relation/utils/is-relation';
import { addTrait, removeTrait } from '../trait/trait';
import { getTraitInstance } from '../trait/trait-instance';
import type { ConfigurableTrait, Trait } from '../trait/types';
import type { World } from '../world';
import { beginEventLog } from './event-log';
import type {
    DeferredBuffer,
    DeferredCommand,
    DeferredCommands,
    DeferredEntry,
    DeferredState,
    PairEntry,
    PendingAdds,
} from './types';

function createBuffer(): DeferredBuffer {
    return {
        commands: [],
        entities: new Set(),
        spawned: new Set(),
        nullified: new Set(),
        pendingAdds: new Map(),
    };
}

function clearBuffer(buffer: DeferredBuffer) {
    buffer.commands.length = 0;
    buffer.entities.clear();
    buffer.spawned.clear();
    buffer.nullified.clear();
    buffer.pendingAdds.clear();
}

export function createDeferredState(): DeferredState {
    return {
        stack: [createBuffer()],
        pool: [],
        reserved: new Set(),
        pending: 0,
        version: 0,
        view: null,
        viewVersion: -1,
    };
}

export function resetDeferredState(state: DeferredState) {
    // Keep the scope depth so scopes opened by an in-progress updateEach still close correctly.
    for (const buffer of state.stack) clearBuffer(buffer);
    state.reserved.clear();
    state.pending = 0;
    state.version++;
    state.view = null;
}

function toEntries(traits: (ConfigurableTrait | Trait | RelationPair)[]): DeferredEntry[] {
    const entries: DeferredEntry[] = [];
    for (const config of traits) {
        if (isRelationPair(config)) {
            const { relation, target, params } = config[$internal];
            entries.push({
                kind: 'pair',
                relation: relation as PairEntry['relation'],
                target,
                params,
            });
        } else if (Array.isArray(config)) {
            entries.push({ kind: 'trait', trait: config[0], params: config[1] });
        } else {
            entries.push({ kind: 'trait', trait: config as Trait, params: undefined });
        }
    }
    return entries;
}

function getPendingAdds(buffer: DeferredBuffer, entity: Entity): PendingAdds {
    let pending = buffer.pendingAdds.get(entity);
    if (!pending) {
        pending = { traits: new Map(), pairs: new Map() };
        buffer.pendingAdds.set(entity, pending);
    }
    return pending;
}

/**
 * Later values for a trait replace the values of an add that is still pending in the
 * buffer. The later add is dropped since it would be a no-op on execution.
 */
function trackAdds(buffer: DeferredBuffer, entity: Entity, entries: DeferredEntry[]) {
    const pending = getPendingAdds(buffer, entity);
    const kept: DeferredEntry[] = [];

    for (const entry of entries) {
        if (entry.kind === 'trait') {
            const existing = pending.traits.get(entry.trait);
            if (existing) {
                existing.params = entry.params;
                continue;
            }
            pending.traits.set(entry.trait, entry);
            kept.push(entry);
            continue;
        }

        const target = entry.target;
        if (typeof target !== 'number') continue;

        let targets = pending.pairs.get(entry.relation);
        if (!targets) {
            targets = new Map();
            pending.pairs.set(entry.relation, targets);
        }

        const existing = targets.get(target);
        if (existing) {
            existing.params = entry.params;
            continue;
        }

        if (entry.relation[$internal].exclusive) targets.clear();
        targets.set(target, entry);
        kept.push(entry);
    }

    return kept;
}

function untrackRemoves(buffer: DeferredBuffer, entity: Entity, entries: DeferredEntry[]) {
    const pending = buffer.pendingAdds.get(entity);
    if (!pending) return;

    for (const entry of entries) {
        if (entry.kind === 'trait') {
            const relation = entry.trait[$internal].relation;
            if (relation) pending.pairs.delete(relation);
            else pending.traits.delete(entry.trait);
        } else if (entry.target === '*') {
            pending.pairs.delete(entry.relation);
        } else {
            pending.pairs.get(entry.relation)?.delete(entry.target);
        }
    }
}

function enqueue(state: DeferredState, buffer: DeferredBuffer, command: DeferredCommand) {
    buffer.commands.push(command);
    buffer.entities.add(command.entity);
    state.pending++;
    state.version++;
}

function getExclusionFlag(world: World) {
    return getTraitInstance(world[$internal].traitInstances, IsExcluded)!;
}

/**
 * Allocates an entity id for a deferred spawn. The entity is excluded from all queries
 * until the spawn executes.
 */
function reserveEntity(world: World): Entity {
    const ctx = world[$internal];
    const entity = allocateEntity(ctx.entityIndex);
    const { generationId, bitflag } = getExclusionFlag(world);

    ctx.entityMasks[generationId][getEntityId(entity)] |= bitflag;
    ctx.entityTraits.set(entity, new Set([IsExcluded]));
    ctx.deferred.reserved.add(entity);

    return entity;
}

function unreserveEntity(world: World, entity: Entity) {
    const ctx = world[$internal];
    const { generationId, bitflag } = getExclusionFlag(world);

    ctx.entityMasks[generationId][getEntityId(entity)] &= ~bitflag;
    ctx.entityTraits.get(entity)!.delete(IsExcluded);
    ctx.deferred.reserved.delete(entity);
}

function toConfigs(entries: DeferredEntry[], isLive: (entity: Entity) => boolean) {
    const configs: ConfigurableTrait[] = [];
    for (const entry of entries) {
        if (entry.kind === 'trait') {
            configs.push(entry.params === undefined ? entry.trait : [entry.trait, entry.params]);
        } else if (typeof entry.target === 'number' && isLive(entry.target)) {
            configs.push(entry.relation(entry.target, entry.params));
        }
    }
    return configs;
}

function toRemovables(entries: DeferredEntry[]) {
    return entries.map((entry) =>
        entry.kind === 'trait' ? entry.trait : entry.relation(entry.target)
    );
}

function executeCommand(world: World, buffer: DeferredBuffer, command: DeferredCommand) {
    const ctx = world[$internal];
    const state = ctx.deferred;
    const entity = command.entity;
    const isLive = (e: Entity) => isEntityAlive(ctx.entityIndex, e) && !buffer.nullified.has(e);

    switch (command.type) {
        case 'spawn': {
            if (buffer.nullified.has(entity) || !state.reserved.has(entity)) return;
            unreserveEntity(world, entity);
            initializeEntity(world, entity, toConfigs(command.entries, isLive));
            return;
        }
        case 'add': {
            if (!isLive(entity)) return;
            addTrait(world, entity, ...toConfigs(command.entries, isLive));
            return;
        }
        case 'remove': {
            if (!isLive(entity)) return;
            removeTrait(world, entity, ...toRemovables(command.entries));
            return;
        }
        case 'addExclusive': {
            if (!isLive(entity)) return;
            const { relation, target, params } = command.entry;

            if (target === '*') {
                removeTrait(world, entity, relation('*'));
                return;
            }
            if (!isLive(target)) return;

            for (const old of getRelationTargets(world, relation, entity)) {
                if (old !== target) removeTrait(world, entity, relation(old));
            }
            addTrait(world, entity, relation(target, params));
            return;
        }
        case 'destroy': {
            if (entity === ctx.worldEntity) {
                throw new Error('Koota: The world entity cannot be destroyed.');
            }

            if (state.reserved.has(entity)) {
                // Never spawned, but relations from other scopes may point at it.
                unreserveEntity(world, entity);
                destroyEntity(world, entity);
            } else if (isEntityAlive(ctx.entityIndex, entity)) {
                destroyEntity(world, entity);
            }
            return;
        }
    }
}

function runBuffer(world: World, buffer: DeferredBuffer) {
    const state = world[$internal].deferred;

    if (buffer.commands.length > 0) {
        state.pending -= buffer.commands.length;
        state.version++;

        const finishEvents = beginEventLog();
        try {
            for (const command of buffer.commands) executeCommand(world, buffer, command);
        } finally {
            // Release reservations left behind by a command that threw.
            for (const entity of buffer.spawned) {
                if (!state.reserved.has(entity)) continue;
                unreserveEntity(world, entity);
                destroyEntity(world, entity);
            }
            finishEvents();
        }
    }

    clearBuffer(buffer);
    state.pool.push(buffer);
}

function detachBuffer(state: DeferredState, index: number) {
    const buffer = state.stack[index];
    state.stack[index] = state.pool.pop() ?? createBuffer();
    return buffer;
}

export function pushDeferredScope(world: World) {
    const state = world[$internal].deferred;
    state.stack.push(state.pool.pop() ?? createBuffer());
}

export function popDeferredScope(world: World) {
    const state = world[$internal].deferred;
    if (state.stack.length <= 1) return;
    runBuffer(world, state.stack.pop()!);
}

export function flushDeferredScope(world: World) {
    const state = world[$internal].deferred;
    runBuffer(world, detachBuffer(state, state.stack.length - 1));
}

/**
 * Called before a non-deferred mutation. Executes every scope from the outermost one
 * holding commands for the entity, so the mutation applies on top of them.
 */
export function flushDeferredFor(world: World, entity: Entity) {
    const state = world[$internal].deferred;
    if (state.pending === 0) return;

    state.version++;

    let index = -1;
    for (let i = 0; i < state.stack.length; i++) {
        if (state.stack[i].entities.has(entity)) {
            index = i;
            break;
        }
    }
    if (index === -1) return;

    for (let i = index; i < state.stack.length; i++) {
        runBuffer(world, detachBuffer(state, i));
    }
}

export function createDeferredCommands(world: World): DeferredCommands {
    const isAlive = (entity: Entity) => isEntityAlive(world[$internal].entityIndex, entity);
    const current = () => {
        const stack = world[$internal].deferred.stack;
        return stack[stack.length - 1];
    };

    return {
        spawn(...traits: ConfigurableTrait[]) {
            const state = world[$internal].deferred;
            const buffer = current();
            const entity = reserveEntity(world);

            buffer.spawned.add(entity);
            const entries = trackAdds(buffer, entity, toEntries(traits));
            enqueue(state, buffer, { type: 'spawn', entity, entries });

            return entity;
        },

        destroy(entity: Entity) {
            if (!isAlive(entity)) return;
            const buffer = current();

            if (buffer.spawned.has(entity)) buffer.nullified.add(entity);
            buffer.pendingAdds.delete(entity);
            enqueue(world[$internal].deferred, buffer, { type: 'destroy', entity });
        },

        add(entity: Entity, ...traits: ConfigurableTrait[]) {
            if (!isAlive(entity)) return;
            const buffer = current();

            const entries = trackAdds(buffer, entity, toEntries(traits));
            if (entries.length === 0) return;
            enqueue(world[$internal].deferred, buffer, { type: 'add', entity, entries });
        },

        remove(entity: Entity, ...traits: (Trait | RelationPair)[]) {
            if (!isAlive(entity)) return;
            const buffer = current();

            const entries = toEntries(traits);
            if (entries.length === 0) return;
            untrackRemoves(buffer, entity, entries);
            enqueue(world[$internal].deferred, buffer, { type: 'remove', entity, entries });
        },

        addExclusive(entity: Entity, pair: RelationPair) {
            if (!isAlive(entity)) return;
            const buffer = current();
            const entry = toEntries([pair])[0] as PairEntry;
            const pending = getPendingAdds(buffer, entity);

            if (entry.target === '*') {
                pending.pairs.delete(entry.relation);
            } else {
                const existing = pending.pairs.get(entry.relation)?.get(entry.target);
                if (existing) existing.params = entry.params;
                pending.pairs.set(entry.relation, new Map([[entry.target, existing ?? entry]]));
            }

            enqueue(world[$internal].deferred, buffer, { type: 'addExclusive', entity, entry });
        },

        flush() {
            flushDeferredScope(world);
        },
    };
}

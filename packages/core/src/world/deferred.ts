import { $internal } from '../common';
import { createEntity, destroyEntity } from '../entity/entity';
import type { Entity } from '../entity/types';
import { isEntityAlive } from '../entity/utils/entity-index';
import { hasRelationPair } from '../relation/relation';
import type { Relation, RelationPair, RelationTarget } from '../relation/types';
import { isRelationPair } from '../relation/utils/is-relation';
import { getSchemaDefaults } from '../storage';
import { addTrait, getTrait, hasTrait, removeTrait } from '../trait/trait';
import { getTraitInstance } from '../trait/trait-instance';
import type { ConfigurableTrait, Trait } from '../trait/types';
import { IsExcluded } from '../query/query';
import type { DeferredCommand, DeferredState, DeferredWorld, World } from './types';

function state(world: World): DeferredState {
    return world[$internal].deferred;
}

function traitFromConfig(config: ConfigurableTrait): Trait | RelationPair {
    return Array.isArray(config) ? config[0] : config;
}

function pendingFor(world: World, entity: Entity): DeferredCommand[] {
    return state(world).commands.filter((command) => command.entity === entity);
}

export function projectedAlive(world: World, entity: Entity): boolean {
    let alive = isEntityAlive(world[$internal].entityIndex, entity);
    for (const command of pendingFor(world, entity)) {
        if (command.kind === 'spawn') alive = true;
        if (command.kind === 'destroy') alive = false;
    }
    return alive;
}

export function projectedHas(
    world: World,
    entity: Entity,
    target: Trait | RelationPair
): boolean {
    if (!projectedAlive(world, entity)) return false;
    let present = isRelationPair(target)
        ? hasRelationPair(world, entity, target)
        : hasTrait(world, entity, target);

    for (const command of pendingFor(world, entity)) {
        if (command.kind === 'spawn') {
            present = false;
            for (const config of command.traits) {
                if (traitFromConfig(config) === target) present = true;
            }
        } else if (command.kind === 'add') {
            for (const config of command.traits) {
                const value = traitFromConfig(config);
                if (value === target) present = true;
                if (isRelationPair(target) && isRelationPair(value)) {
                    if (value[$internal].relation === target[$internal].relation) {
                        if (target[$internal].target === '*') present = true;
                        else if (value[$internal].target === target[$internal].target) present = true;
                    }
                }
            }
        } else if (command.kind === 'remove') {
            for (const value of command.traits) {
                if (value === target || (isRelationPair(value) && isRelationPair(target) &&
                    value[$internal].relation === target[$internal].relation &&
                    (value[$internal].target === '*' || value[$internal].target === target[$internal].target))) {
                    present = false;
                }
            }
        } else if (command.kind === 'exclusive' && isRelationPair(target)) {
            if (command.pair[$internal].relation === target[$internal].relation) {
                present =
                    target[$internal].target === '*' ||
                    target[$internal].target === command.pair[$internal].target;
            }
        }
    }
    return present;
}

export function projectedGet(world: World, entity: Entity, target: Trait | RelationPair) {
    if (!projectedHas(world, entity, target)) return undefined;
    let value = getTrait(world, entity, target);
    for (const command of pendingFor(world, entity)) {
        const configs =
            command.kind === 'spawn' || command.kind === 'add' ? command.traits : [];
        for (const config of configs) {
            const candidate = traitFromConfig(config);
            if (candidate !== target) continue;
            if (isRelationPair(candidate)) {
                const params = candidate[$internal].params;
                const relationTrait = candidate[$internal].relation[$internal].trait;
                const instance = getTraitInstance(world[$internal].traitInstances, relationTrait);
                const defaults = instance && getSchemaDefaults(instance.schema, relationTrait[$internal].type);
                value = defaults ? { ...defaults, ...params } : params;
            } else {
                const params = Array.isArray(config) ? config[1] : undefined;
                const defaults = getSchemaDefaults(candidate.schema, candidate[$internal].type);
                value = defaults ? { ...defaults, ...params } : params ?? value;
            }
        }
    }
    return value;
}

function commandAffects(command: DeferredCommand, entity: Entity): boolean {
    return command.entity === entity;
}

function sameTrait(left: ConfigurableTrait, right: ConfigurableTrait): boolean {
    const a = traitFromConfig(left);
    const b = traitFromConfig(right);
    if (isRelationPair(a) && isRelationPair(b)) {
        return a[$internal].relation === b[$internal].relation &&
            a[$internal].target === b[$internal].target;
    }
    return a === b;
}

export function flushEntity(world: World, entity: Entity): void {
    if (state(world).flushing) return;
    const commands = state(world).commands;
    const selected = commands.filter((command) => commandAffects(command, entity));
    if (selected.length === 0) return;
    state(world).commands = commands.filter((command) => !commandAffects(command, entity));
    execute(world, selected);
}

export function flushRange(world: World, start: number): void {
    const commands = state(world).commands.splice(start);
    execute(world, commands);
}

function execute(world: World, commands: DeferredCommand[]): void {
    const ctx = world[$internal];
    const previous = ctx.deferred.flushing;
    ctx.deferred.flushing = true;
    try {
        const destroyed = new Set<Entity>();
        const spawned = new Set<Entity>();
        for (const command of commands) {
            if (command.kind === 'spawn') spawned.add(command.entity);
            if (command.kind === 'destroy') {
                if (spawned.has(command.entity)) destroyed.add(command.entity);
                else if (command.entity === ctx.worldEntity) {
                    throw new Error('Koota: The world entity cannot be destroyed through deferred commands.');
                }
            }
        }
        for (let commandIndex = 0; commandIndex < commands.length; commandIndex++) {
            const command = commands[commandIndex];
            if (destroyed.has(command.entity)) {
                if (command.kind === 'spawn') destroyEntity(world, command.entity);
                continue;
            }
            if (command.kind === 'spawn') {
                const traits = command.traits.filter(
                    (config) =>
                        !commands.slice(commandIndex + 1).some(
                            (later) =>
                                (later.kind === 'add' || later.kind === 'spawn') &&
                                later.entity === command.entity &&
                                later.traits.some((candidate) => sameTrait(candidate, config))
                        )
                );
                addTrait(world, command.entity, ...traits);
                removeTrait(world, command.entity, IsExcluded);
            } else if (!projectedAlive(world, command.entity)) {
                continue;
            } else if (command.kind === 'destroy') {
                destroyEntity(world, command.entity);
            } else if (command.kind === 'add') {
                const traits = command.traits.filter(
                    (config) =>
                        !commands.slice(commandIndex + 1).some(
                            (later) =>
                                (later.kind === 'add' || later.kind === 'spawn') &&
                                later.entity === command.entity &&
                                later.traits.some((candidate) => sameTrait(candidate, config))
                        )
                );
                addTrait(world, command.entity, ...traits);
            } else if (command.kind === 'remove') {
                removeTrait(world, command.entity, ...command.traits);
            } else {
                removeTrait(world, command.entity, command.pair[$internal].relation('*'));
                addTrait(world, command.entity, command.pair);
            }
        }
    } finally {
        ctx.deferred.flushing = previous;
    }
}

export function createDeferred(world: World): DeferredWorld {
    const enqueue = (command: DeferredCommand) => state(world).commands.push(command);
    return {
        spawn(...traits) {
            const entity = createEntity(world, IsExcluded);
            enqueue({ kind: 'spawn', entity, traits });
            return entity;
        },
        destroy(entity) {
            enqueue({ kind: 'destroy', entity });
        },
        add(entity, ...traits) {
            enqueue({ kind: 'add', entity, traits });
        },
        remove(entity, ...traits) {
            enqueue({ kind: 'remove', entity, traits });
        },
        addExclusive(entity, pairOrRelation, target?, params?) {
            const pair = isRelationPair(pairOrRelation)
                ? pairOrRelation
                : pairOrRelation(target as RelationTarget, params);
            enqueue({ kind: 'exclusive', entity, pair });
        },
        flush() {
            const commands = state(world).commands.splice(0);
            execute(world, commands);
        },
    };
}

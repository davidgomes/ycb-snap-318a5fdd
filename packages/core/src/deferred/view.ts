import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { isEntityAlive } from '../entity/utils/entity-index';
import { IsExcluded } from '../query/query';
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
import { getTrait, hasTrait } from '../trait/trait';
import type { Trait } from '../trait/types';
import type { World } from '../world';
import type { DeferredBuffer, DeferredEntry } from './types';

type ViewState = { present: boolean; params: any; value?: unknown; resolved: boolean };

type ViewRelation = { cleared: boolean; targets: Map<Entity, ViewState> };

type ViewEntity = {
    alive: boolean;
    traits: Map<Trait, ViewState>;
    relations: Map<Relation<Trait>, ViewRelation>;
};

/**
 * The state entities will have once all pending commands execute, layered over the
 * current world state. Only entities touched by the replay are stored.
 */
export type DeferredView = {
    entities: Map<Entity, ViewEntity>;
    relations: Set<Relation<Trait>>;
};

function isRealAlive(world: World, entity: Entity) {
    return isEntityAlive(world[$internal].entityIndex, entity);
}

function getViewEntity(world: World, view: DeferredView, entity: Entity) {
    let viewEntity = view.entities.get(entity);
    if (!viewEntity) {
        viewEntity = {
            alive: isRealAlive(world, entity),
            traits: new Map(),
            relations: new Map(),
        };
        view.entities.set(entity, viewEntity);
    }
    return viewEntity;
}

function getViewRelation(
    world: World,
    view: DeferredView,
    entity: Entity,
    relation: Relation<Trait>
) {
    view.relations.add(relation);
    const viewEntity = getViewEntity(world, view, entity);
    let viewRelation = viewEntity.relations.get(relation);
    if (!viewRelation) {
        viewRelation = { cleared: false, targets: new Map() };
        viewEntity.relations.set(relation, viewRelation);
    }
    return viewRelation;
}

function isAlive(world: World, view: DeferredView, entity: Entity) {
    const viewEntity = view.entities.get(entity);
    return viewEntity ? viewEntity.alive : isRealAlive(world, entity);
}

function viewHasTrait(world: World, view: DeferredView, entity: Entity, trait: Trait): boolean {
    const relation = trait[$internal].relation;
    if (relation) return viewTargets(world, view, entity, relation).length > 0;

    const viewEntity = view.entities.get(entity);
    if (viewEntity) {
        if (!viewEntity.alive) return false;
        const state = viewEntity.traits.get(trait);
        if (state) return state.present;
    } else if (!isRealAlive(world, entity)) {
        return false;
    }

    if (trait === IsExcluded && world[$internal].deferred.reserved.has(entity)) return false;
    return hasTrait(world, entity, trait);
}

function viewTargets(
    world: World,
    view: DeferredView,
    entity: Entity,
    relation: Relation<Trait>
): Entity[] {
    const viewEntity = view.entities.get(entity);
    if (viewEntity ? !viewEntity.alive : !isRealAlive(world, entity)) return [];

    const viewRelation = viewEntity?.relations.get(relation);
    const result: Entity[] = [];

    if (!viewRelation?.cleared) {
        for (const target of getRelationTargets(world, relation, entity)) {
            const state = viewRelation?.targets.get(target);
            if (!state || state.present) result.push(target);
        }
    }

    if (viewRelation) {
        for (const [target, state] of viewRelation.targets) {
            if (state.present && !result.includes(target)) result.push(target);
        }
    }

    return result;
}

function viewHasPair(
    world: World,
    view: DeferredView,
    entity: Entity,
    relation: Relation<Trait>,
    target: Entity
): boolean {
    const viewEntity = view.entities.get(entity);
    if (viewEntity) {
        if (!viewEntity.alive) return false;
        const viewRelation = viewEntity.relations.get(relation);
        const state = viewRelation?.targets.get(target);
        if (state) return state.present;
        if (viewRelation?.cleared) return false;
    } else if (!isRealAlive(world, entity)) {
        return false;
    }

    return hasRelationToTarget(world, relation, entity, target);
}

function viewSources(world: World, view: DeferredView, relation: Relation<Trait>, target: Entity) {
    const result: Entity[] = [];

    for (const source of getEntitiesWithRelationTo(world, relation, target)) {
        if (viewHasPair(world, view, source, relation, target)) result.push(source);
    }

    for (const [source, viewEntity] of view.entities) {
        if (!viewEntity.alive || result.includes(source)) continue;
        if (viewEntity.relations.get(relation)?.targets.get(target)?.present) result.push(source);
    }

    return result;
}

function setTraitState(
    world: World,
    view: DeferredView,
    entity: Entity,
    trait: Trait,
    present: boolean,
    params?: unknown
) {
    getViewEntity(world, view, entity).traits.set(trait, { present, params, resolved: false });
}

function setPairState(
    world: World,
    view: DeferredView,
    entity: Entity,
    relation: Relation<Trait>,
    target: Entity,
    present: boolean,
    params?: unknown
) {
    const viewRelation = getViewRelation(world, view, entity, relation);
    viewRelation.targets.set(target, { present, params, resolved: false });
}

function clearRelation(world: World, view: DeferredView, entity: Entity, relation: Relation<Trait>) {
    const viewRelation = getViewRelation(world, view, entity, relation);
    viewRelation.cleared = true;
    viewRelation.targets.clear();
}

function addPair(
    world: World,
    view: DeferredView,
    entity: Entity,
    relation: Relation<Trait>,
    target: Entity,
    params: unknown
) {
    view.relations.add(relation);
    if (viewHasPair(world, view, entity, relation, target)) return;

    if (relation[$internal].exclusive) {
        const old = viewTargets(world, view, entity, relation)[0];
        if (old !== undefined) setPairState(world, view, entity, relation, old, false);
    }

    setPairState(world, view, entity, relation, target, true, params);
}

/** Mirrors destroyEntity, including autoDestroy cascades. */
function destroy(world: World, view: DeferredView, entity: Entity) {
    const queue = [entity];
    const processed = new Set<Entity>();

    while (queue.length > 0) {
        const current = queue.pop()!;
        if (processed.has(current)) continue;
        processed.add(current);

        const relations = new Set([...world[$internal].relations, ...view.relations]);

        for (const relation of relations) {
            const autoDestroy = relation[$internal].autoDestroy;

            for (const source of viewSources(world, view, relation, current)) {
                if (!isAlive(world, view, source)) continue;
                setPairState(world, view, source, relation, current, false);
                if (autoDestroy === 'source') queue.push(source);
            }

            if (autoDestroy === 'target') {
                for (const target of viewTargets(world, view, current, relation)) {
                    if (!isAlive(world, view, target)) continue;
                    if (!processed.has(target)) queue.push(target);
                }
            }
        }

        const viewEntity = getViewEntity(world, view, current);
        viewEntity.alive = false;
        viewEntity.traits.clear();
        viewEntity.relations.clear();
    }
}

function addEntries(
    world: World,
    view: DeferredView,
    entity: Entity,
    entries: DeferredEntry[],
    isLive: (entity: Entity) => boolean
) {
    for (const entry of entries) {
        if (entry.kind === 'pair') {
            if (typeof entry.target !== 'number' || !isLive(entry.target)) continue;
            addPair(world, view, entity, entry.relation, entry.target, entry.params);
        } else if (!viewHasTrait(world, view, entity, entry.trait)) {
            setTraitState(world, view, entity, entry.trait, true, entry.params);
        }
    }
}

function removeEntries(world: World, view: DeferredView, entity: Entity, entries: DeferredEntry[]) {
    for (const entry of entries) {
        if (entry.kind === 'trait') {
            const relation = entry.trait[$internal].relation;
            if (relation) clearRelation(world, view, entity, relation);
            else if (viewHasTrait(world, view, entity, entry.trait)) {
                setTraitState(world, view, entity, entry.trait, false);
            }
        } else if (entry.target === '*') {
            clearRelation(world, view, entity, entry.relation);
        } else if (
            typeof entry.target === 'number' &&
            viewHasPair(world, view, entity, entry.relation, entry.target)
        ) {
            setPairState(world, view, entity, entry.relation, entry.target, false);
        }
    }
}

/** Returns false if the buffer throws on execution, which stops the remaining replay. */
function replayBuffer(world: World, view: DeferredView, buffer: DeferredBuffer): boolean {
    const ctx = world[$internal];
    const isLive = (e: Entity) => isAlive(world, view, e) && !buffer.nullified.has(e);

    for (const command of buffer.commands) {
        const entity = command.entity;

        switch (command.type) {
            case 'spawn':
                if (!isLive(entity)) break;
                addEntries(world, view, entity, command.entries, isLive);
                break;
            case 'add':
                if (!isLive(entity)) break;
                addEntries(world, view, entity, command.entries, isLive);
                break;
            case 'remove':
                if (!isLive(entity)) break;
                removeEntries(world, view, entity, command.entries);
                break;
            case 'addExclusive': {
                if (!isLive(entity)) break;
                const { relation, target, params } = command.entry;
                if (target === '*') {
                    clearRelation(world, view, entity, relation);
                    break;
                }
                if (!isLive(target)) break;
                for (const old of viewTargets(world, view, entity, relation)) {
                    if (old !== target) setPairState(world, view, entity, relation, old, false);
                }
                addPair(world, view, entity, relation, target, params);
                break;
            }
            case 'destroy':
                if (entity === ctx.worldEntity) return false;
                if (isAlive(world, view, entity)) destroy(world, view, entity);
                break;
        }
    }

    return true;
}

function getView(world: World): DeferredView {
    const state = world[$internal].deferred;
    if (state.view && state.viewVersion === state.version) return state.view;

    const view: DeferredView = { entities: new Map(), relations: new Set() };

    // Scopes execute innermost first as each one closes.
    for (let i = state.stack.length - 1; i >= 0; i--) {
        if (!replayBuffer(world, view, state.stack[i])) break;
    }

    state.view = view;
    state.viewVersion = state.version;
    return view;
}

function resolveTraitValue(world: World, entity: Entity, trait: Trait, state: ViewState) {
    if (state.resolved) return state.value;

    const type = trait[$internal].type;
    const params = state.params;
    let value: unknown;

    if (type === 'aos') {
        value =
            params ??
            (isOrderedTrait(trait)
                ? new OrderedList(world, entity, getOrderedTraitRelation(trait), trait)
                : getSchemaDefaults(trait.schema, type));
    } else if (type === 'soa') {
        const merged = { ...getSchemaDefaults(trait.schema, type), ...params };
        const record: Record<string, unknown> = {};
        for (const key in trait.schema) record[key] = merged[key];
        value = record;
    }

    state.value = value;
    state.resolved = true;
    return value;
}

function resolvePairValue(relation: Relation<Trait>, state: ViewState) {
    if (state.resolved) return state.value;

    const trait = relation[$internal].trait;
    const type = trait[$internal].type;
    const defaults = getSchemaDefaults(trait.schema, type);
    const stored = defaults ? { ...defaults, ...state.params } : state.params;
    let value: unknown = stored;

    if (type !== 'aos') {
        const record: Record<string, unknown> = {};
        for (const key in trait.schema) record[key] = stored?.[key];
        value = record;
    }

    state.value = value;
    state.resolved = true;
    return value;
}

export function hasDeferred(world: World, entity: Entity, trait: Trait | RelationPair): boolean {
    const view = getView(world);

    if (isRelationPair(trait)) {
        const { relation, target } = trait[$internal];
        if (target === '*')
            return viewTargets(world, view, entity, relation as Relation<Trait>).length > 0;
        if (typeof target !== 'number') return false;
        return viewHasPair(world, view, entity, relation as Relation<Trait>, target);
    }

    return viewHasTrait(world, view, entity, trait);
}

export function getDeferred(world: World, entity: Entity, trait: Trait | RelationPair) {
    const view = getView(world);
    const viewEntity = view.entities.get(entity);

    if (isRelationPair(trait)) {
        const relation = trait[$internal].relation as Relation<Trait>;
        const target = trait[$internal].target;
        if (typeof target !== 'number') return undefined;
        if (!viewHasPair(world, view, entity, relation, target)) return undefined;

        const state = viewEntity?.relations.get(relation)?.targets.get(target);
        if (state?.present) return resolvePairValue(relation, state);
        return getRelationData(world, entity, relation, target);
    }

    if (!viewHasTrait(world, view, entity, trait)) return undefined;

    const state = viewEntity?.traits.get(trait);
    if (state?.present) return resolveTraitValue(world, entity, trait, state);
    return getTrait(world, entity, trait);
}

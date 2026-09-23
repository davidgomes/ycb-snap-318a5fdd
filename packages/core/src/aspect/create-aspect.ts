import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { setChanged } from '../query/modifiers/changed';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import {
    addTrait,
    getTrait,
    hasTrait,
    registerTrait,
    removeTrait,
    setAddAspectHandler,
    setRemoveAspectHandler,
    setTrait,
    trait,
} from '../trait/trait';
import { getTraitInstance, hasTraitInstance } from '../trait/trait-instance';
import type { Trait } from '../trait/types';
import { universe } from '../universe/universe';
import type { World } from '../world/types';
import { $aspect, isAspect } from './is-aspect';
import type { Aspect, AspectInternal, FlattenList } from './types';
import {
    getAspectWatchers,
    registerAspectWatcher,
    setAspectAddedHandler,
    setAspectChangedHandler,
    setAspectRemovingHandler,
} from './watchers';

let aspectId = 0;
const preparedAspects = new WeakMap<World, Set<number>>();
const resetHooked = new WeakSet<World>();

function traitFieldNames(constituent: Trait): string[] {
    const ctx = constituent[$internal];
    if (ctx.type === 'tag') return [];
    if (ctx.type === 'aos') {
        const schema = constituent.schema;
        if (typeof schema !== 'function') return [];
        const sample = schema();
        if (sample && typeof sample === 'object') return Object.keys(sample as object);
        return [];
    }
    return Object.keys(constituent.schema as object);
}

function flattenConstituents(inputs: readonly (Trait | Aspect)[]): Trait[] {
    const flat: Trait[] = [];
    const seen = new Set<number>();

    const visit = (input: Trait | Aspect) => {
        if (isAspect(input)) {
            const nested = input.traits;
            for (let i = 0; i < nested.length; i++) visit(nested[i]);
            return;
        }
        if (isRelation(input) || isRelationPair(input)) {
            throw new Error('Koota: Aspects cannot contain relations.');
        }
        if (typeof input !== 'function' || !(input as Trait)[$internal]) {
            throw new Error('Koota: createAspect expects traits or aspects.');
        }
        const constituent = input as Trait;
        if (constituent[$internal].relation) {
            throw new Error('Koota: Aspects cannot contain relations.');
        }
        if (seen.has(constituent.id)) return;
        seen.add(constituent.id);
        flat.push(constituent);
    };

    for (let i = 0; i < inputs.length; i++) visit(inputs[i]);

    if (flat.length < 2) {
        throw new Error('Koota: createAspect requires two or more traits.');
    }

    return flat;
}

function hasEveryTrait(world: World, entity: Entity, traits: readonly Trait[]): boolean {
    for (let i = 0; i < traits.length; i++) {
        if (!hasTrait(world, entity, traits[i])) return false;
    }
    return true;
}

export function hasAspect(world: World, entity: Entity, aspect: Aspect): boolean {
    return hasEveryTrait(world, entity, aspect.traits);
}

export function getAspect(
    world: World,
    entity: Entity,
    aspect: Aspect
): Record<string, unknown> | undefined {
    if (!hasAspect(world, entity, aspect)) return undefined;

    const merged: Record<string, unknown> = {};
    const parts = aspect[$internal].parts;
    for (let i = 0; i < parts.length; i++) {
        const part = parts[i];
        const value = getTrait(world, entity, part.trait);
        if (part.trait[$internal].type === 'aos') {
            if (value && typeof value === 'object') {
                const keys = part.keys;
                for (let k = 0; k < keys.length; k++) {
                    const key = keys[k];
                    merged[key] = (value as Record<string, unknown>)[key];
                }
            }
            continue;
        }
        if (value && typeof value === 'object') Object.assign(merged, value);
    }
    return merged;
}

export function setAspect(
    world: World,
    entity: Entity,
    aspect: Aspect,
    value: Record<string, unknown> | ((prev: Record<string, unknown>) => Record<string, unknown>),
    triggerChanged = true
) {
    if (!hasAspect(world, entity, aspect)) return;

    const next = typeof value === 'function' ? value(getAspect(world, entity, aspect)!) : value;
    const fieldOwners = aspect[$internal].fieldOwners;
    const grouped = new Map<Trait, Record<string, unknown>>();
    const keys = Object.keys(next);

    for (let i = 0; i < keys.length; i++) {
        const key = keys[i];
        const owner = fieldOwners.get(key);
        if (!owner) continue;
        let bucket = grouped.get(owner);
        if (!bucket) {
            bucket = {};
            grouped.set(owner, bucket);
        }
        bucket[key] = next[key];
    }

    for (const [constituent, partial] of grouped) {
        if (constituent[$internal].type === 'aos') {
            const current = getTrait(world, entity, constituent);
            if (!current || typeof current !== 'object') continue;
            const record = current as Record<string, unknown>;
            let changed = false;
            for (const key in partial) {
                if (record[key] !== partial[key]) {
                    record[key] = partial[key];
                    changed = true;
                }
            }
            if (changed && triggerChanged) setChanged(world, entity, constituent);
            continue;
        }
        setTrait(world, entity, constituent, partial, triggerChanged);
    }
}

export function addAspect(
    world: World,
    entity: Entity,
    aspect: Aspect,
    params?: Record<string, unknown>
) {
    const traits = aspect.traits;
    const fieldOwners = aspect[$internal].fieldOwners;

    for (let i = 0; i < traits.length; i++) {
        const constituent = traits[i];
        if (hasTrait(world, entity, constituent)) continue;

        if (!params) {
            addTrait(world, entity, constituent);
            continue;
        }

        const partial: Record<string, unknown> = {};
        let hasField = false;
        const paramKeys = Object.keys(params);
        for (let k = 0; k < paramKeys.length; k++) {
            const key = paramKeys[k];
            if (fieldOwners.get(key) === constituent) {
                partial[key] = params[key];
                hasField = true;
            }
        }

        if (!hasField) {
            addTrait(world, entity, constituent);
            continue;
        }

        if (constituent[$internal].type === 'aos') {
            const schema = constituent.schema;
            const defaults = typeof schema === 'function' ? schema() : {};
            const initial =
                defaults && typeof defaults === 'object'
                    ? Object.assign(defaults as object, partial)
                    : partial;
            addTrait(world, entity, [constituent, initial]);
        } else {
            addTrait(world, entity, [constituent, partial]);
        }
    }

    const completeness = aspect[$internal].completeness;
    if (hasAspect(world, entity, aspect) && !hasTrait(world, entity, completeness)) {
        addTrait(world, entity, completeness);
    }
}

export function removeAspect(world: World, entity: Entity, aspect: Aspect) {
    removeTrait(world, entity, ...aspect.traits);
}

function backfillAspect(world: World, aspect: Aspect) {
    const ctx = world[$internal];
    const completeness = aspect[$internal].completeness;
    if (!hasTraitInstance(ctx.traitInstances, completeness)) registerTrait(world, completeness);

    const instance = getTraitInstance(ctx.traitInstances, completeness)!;
    const { generationId, bitflag } = instance;
    const masks = ctx.entityMasks[generationId] ?? (ctx.entityMasks[generationId] = []);
    const entities = world.entities;

    for (let i = 0; i < entities.length; i++) {
        const entity = entities[i];
        if (!hasAspect(world, entity, aspect)) continue;
        const eid = getEntityId(entity);
        if (((masks[eid] ?? 0) & bitflag) === bitflag) continue;

        masks[eid] = (masks[eid] ?? 0) | bitflag;
        ctx.entityTraits.get(entity)?.add(completeness);

        // Treat pre-existing complete entities as already present so Added/Removed
        // cursors created earlier do not see this repair as a transition.
        for (const snapshot of ctx.trackingSnapshots.values()) {
            if (!snapshot[generationId]) snapshot[generationId] = [];
            const row = snapshot[generationId];
            row[eid] = (row[eid] ?? 0) | bitflag;
        }
    }
}

export function ensureAspect(world: World, aspect: Aspect) {
    if (!resetHooked.has(world)) {
        resetHooked.add(world);
        world[$internal].resetSubscriptions.add(() => {
            preparedAspects.delete(world);
        });
    }

    let ids = preparedAspects.get(world);
    if (!ids) {
        ids = new Set();
        preparedAspects.set(world, ids);
    }
    if (ids.has(aspect.id)) return;
    ids.add(aspect.id);
    backfillAspect(world, aspect);
}

function onConstituentAdded(world: World, entity: Entity, constituent: Trait) {
    const bucket = getAspectWatchers(constituent.id);
    if (!bucket) return;
    for (let i = 0; i < bucket.length; i++) {
        const watcher = bucket[i];
        if (hasTrait(world, entity, watcher.completeness)) continue;
        if (!hasEveryTrait(world, entity, watcher.traits)) continue;
        addTrait(world, entity, watcher.completeness);
    }
}

function onConstituentRemoving(world: World, entity: Entity, constituent: Trait) {
    const bucket = getAspectWatchers(constituent.id);
    if (!bucket) return;
    for (let i = 0; i < bucket.length; i++) {
        const watcher = bucket[i];
        if (!hasTrait(world, entity, watcher.completeness)) continue;
        removeTrait(world, entity, watcher.completeness);
    }
}

function onConstituentChanged(world: World, entity: Entity, constituent: Trait) {
    const bucket = getAspectWatchers(constituent.id);
    if (!bucket) return;
    for (let i = 0; i < bucket.length; i++) {
        const watcher = bucket[i];
        if (!hasTrait(world, entity, watcher.completeness)) continue;
        setChanged(world, entity, watcher.completeness);
    }
}

setAspectAddedHandler(onConstituentAdded);
setAspectRemovingHandler(onConstituentRemoving);
setAspectChangedHandler(onConstituentChanged);
setAddAspectHandler(addAspect);
setRemoveAspectHandler(removeAspect);

export function createAspect<A extends Aspect>(aspect: A): Aspect<A['traits']>;
export function createAspect<
    const T extends readonly [Trait | Aspect, Trait | Aspect, ...(Trait | Aspect)[]],
>(...constituents: T): Aspect<FlattenList<T>>;
export function createAspect(...constituents: readonly (Trait | Aspect)[]): Aspect {
    const flat = flattenConstituents(constituents);
    const fieldOwners = new Map<string, Trait>();
    const parts: AspectInternal['parts'] = [];
    const schema: Record<string, unknown> = {};

    for (let i = 0; i < flat.length; i++) {
        const constituent = flat[i];
        const names = traitFieldNames(constituent);
        for (let k = 0; k < names.length; k++) {
            const name = names[k];
            if (fieldOwners.has(name)) {
                throw new Error(`Koota: Aspect field "${name}" is declared on multiple traits.`);
            }
            fieldOwners.set(name, constituent);
        }
        if (names.length > 0) parts.push({ trait: constituent, keys: names });
        if (constituent[$internal].type === 'soa') {
            const traitSchema = constituent.schema as Record<string, unknown>;
            for (const key in traitSchema) schema[key] = traitSchema[key];
        }
    }

    const completeness = trait();
    const internal: AspectInternal = {
        isAspect: true,
        completeness,
        parts,
        fieldOwners,
    };
    const id = aspectId++;
    const traits = Object.freeze(flat.slice());

    const aspect = Object.assign((params?: Record<string, unknown>) => [aspect, params], {
        [$aspect]: true as const,
        [$internal]: internal,
    }) as Aspect;

    Object.defineProperty(aspect, 'id', {
        value: id,
        writable: false,
        enumerable: true,
        configurable: false,
    });
    Object.defineProperty(aspect, 'traits', {
        value: traits,
        writable: false,
        enumerable: true,
        configurable: false,
    });
    Object.defineProperty(aspect, 'schema', {
        value: schema,
        writable: false,
        enumerable: true,
        configurable: false,
    });

    registerAspectWatcher({ traits, completeness });

    for (let i = 0; i < universe.worlds.length; i++) {
        const world = universe.worlds[i];
        if (world) ensureAspect(world, aspect);
    }

    return aspect;
}

import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { isRelation, isRelationPair } from '../relation/utils/is-relation';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait } from '../trait/trait';
import type { ConfigurableTrait, Trait } from '../trait/types';
import type { World } from '../world';
import { $aspect } from './symbols';
import type { Aspect, AspectInput, MergeTraits } from './types';

let aspectId = 1;

function isTrait(value: unknown): value is Trait {
    if (typeof value !== 'function' && typeof value !== 'object') return false;
    const internal = (value as Trait | null | undefined)?.[$internal];
    return !!internal && typeof (value as Trait).id === 'number' && 'schema' in (value as Trait);
}

export function isAspect(value: unknown): value is Aspect {
    return !!value && (value as Aspect)[$aspect] === true;
}

export function isAspectTuple(
    value: unknown
): value is [Aspect, Record<string, unknown> | undefined] {
    return Array.isArray(value) && isAspect(value[0]);
}

function collectTrait(input: unknown, out: Trait[]): void {
    if (isAspect(input)) {
        const traits = input.traits;
        for (let i = 0; i < traits.length; i++) out.push(traits[i]);
        return;
    }

    if (isRelation(input) || isRelationPair(input)) {
        throw new Error('Koota: Aspects cannot contain relations.');
    }

    if (!isTrait(input)) {
        throw new Error('Koota: createAspect expected traits or aspects.');
    }

    if (input[$internal].relation) {
        throw new Error('Koota: Aspects cannot contain relations.');
    }

    if (input[$internal].type === 'aos') {
        throw new Error('Koota: Aspects cannot contain AoS traits.');
    }

    out.push(input);
}

/**
 * Group two or more traits into one aspect.
 * Nested aspects are flattened. Each call returns a distinct instance.
 */
export function createAspect<T extends readonly [AspectInput, ...AspectInput[]]>(
    ...inputs: T
): Aspect<Flattened<T>> {
    if (inputs.length < 1) {
        throw new Error('Koota: createAspect requires two or more traits.');
    }

    const traits: Trait[] = [];
    for (let i = 0; i < inputs.length; i++) collectTrait(inputs[i], traits);

    if (traits.length < 2) {
        throw new Error('Koota: createAspect requires two or more traits.');
    }

    const seen = new Set<number>();
    for (let i = 0; i < traits.length; i++) {
        const id = traits[i].id;
        if (seen.has(id)) throw new Error('Koota: Aspect constituents must be unique.');
        seen.add(id);
    }

    const schema: Record<string, unknown> = {};
    for (let i = 0; i < traits.length; i++) {
        const traitSchema = traits[i].schema as Record<string, unknown>;
        for (const key in traitSchema) {
            if (key in schema) {
                throw new Error(`Koota: Aspect constituents have overlapping field "${key}".`);
            }
            schema[key] = traitSchema[key];
        }
    }

    const id = aspectId++;
    const frozenTraits = Object.freeze(traits.slice());
    const frozenSchema = Object.freeze(schema);

    const aspect = Object.assign(
        (params?: Record<string, unknown>) => [aspect, params] as [Aspect, Record<string, unknown>],
        {}
    ) as Aspect;

    Object.defineProperty(aspect, $aspect, {
        value: true,
        writable: false,
        enumerable: false,
        configurable: false,
    });
    Object.defineProperty(aspect, 'id', {
        value: id,
        writable: false,
        enumerable: true,
        configurable: false,
    });
    Object.defineProperty(aspect, 'traits', {
        value: frozenTraits,
        writable: false,
        enumerable: true,
        configurable: false,
    });
    Object.defineProperty(aspect, 'schema', {
        value: frozenSchema,
        writable: false,
        enumerable: true,
        configurable: false,
    });

    return aspect as Aspect<Flattened<T>>;
}

type FlattenInput<T> = T extends Aspect<infer Traits>
    ? Traits
    : T extends Trait
      ? readonly [T]
      : readonly [];

type Flattened<T extends readonly unknown[]> = T extends readonly [infer Head, ...infer Rest]
    ? readonly [...FlattenInput<Head>, ...Flattened<Rest>]
    : readonly [];

function fieldsFor(
    trait: Trait,
    values: Record<string, unknown>
): Record<string, unknown> | undefined {
    const traitSchema = trait.schema;
    if (!traitSchema || typeof traitSchema !== 'object') return undefined;

    const picked: Record<string, unknown> = {};
    let any = false;
    for (const key in traitSchema) {
        if (key in values) {
            picked[key] = values[key];
            any = true;
        }
    }
    return any ? picked : undefined;
}

export function hasAspect(world: World, entity: Entity, aspect: Aspect): boolean {
    const traits = aspect.traits;
    for (let i = 0; i < traits.length; i++) {
        if (!hasTrait(world, entity, traits[i])) return false;
    }
    return true;
}

export function getAspect(
    world: World,
    entity: Entity,
    aspect: Aspect
): MergeTraits<readonly Trait[]> | undefined {
    const traits = aspect.traits;
    for (let i = 0; i < traits.length; i++) {
        if (!hasTrait(world, entity, traits[i])) return undefined;
    }

    const merged: Record<string, unknown> = {};
    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        if (trait[$internal].type === 'tag') continue;
        const data = getTrait(world, entity, trait);
        if (data && typeof data === 'object') Object.assign(merged, data);
    }
    return merged as MergeTraits<readonly Trait[]>;
}

export function setAspect(
    world: World,
    entity: Entity,
    aspect: Aspect,
    value: Record<string, unknown> | ((prev: Record<string, unknown>) => Record<string, unknown>)
): void {
    let next: Record<string, unknown> | undefined = value as Record<string, unknown>;
    if (typeof value === 'function') {
        const prev = getAspect(world, entity, aspect) ?? {};
        next = value(prev as Record<string, unknown>);
    }
    if (!next || typeof next !== 'object') return;

    const traits = aspect.traits;
    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        if (trait[$internal].type === 'tag') continue;
        if (!hasTrait(world, entity, trait)) continue;
        const picked = fieldsFor(trait, next);
        if (!picked) continue;
        setTrait(world, entity, trait, picked, true);
    }
}

export function addAspect(
    world: World,
    entity: Entity,
    aspect: Aspect,
    values?: Record<string, unknown>
): void {
    const traits = aspect.traits;
    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        if (hasTrait(world, entity, trait)) continue;
        const picked = values ? fieldsFor(trait, values) : undefined;
        if (picked) addTrait(world, entity, [trait, picked] as ConfigurableTrait);
        else addTrait(world, entity, trait);
    }
}

export function removeAspect(world: World, entity: Entity, aspect: Aspect): void {
    removeTrait(world, entity, ...aspect.traits);
}

export function applyAdd(world: World, entity: Entity, inputs: readonly unknown[]): void {
    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];
        if (isAspect(input)) addAspect(world, entity, input);
        else if (isAspectTuple(input)) addAspect(world, entity, input[0], input[1]);
        else addTrait(world, entity, input as ConfigurableTrait);
    }
}

export function applyRemove(world: World, entity: Entity, inputs: readonly unknown[]): void {
    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];
        if (isAspect(input)) removeAspect(world, entity, input);
        else removeTrait(world, entity, input as Trait);
    }
}

type AspectEvent = 'add' | 'remove' | 'change';

export function subscribeAspect(
    world: World,
    event: AspectEvent,
    aspect: Aspect,
    callback: (entity: Entity) => void
): () => void {
    const traits = aspect.traits;
    const unsubs: Array<() => void> = [];

    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        if (event === 'add') {
            unsubs.push(
                world.onAdd(trait, (entity) => {
                    if (hasAspect(world, entity, aspect)) callback(entity);
                })
            );
        } else if (event === 'remove') {
            unsubs.push(
                world.onRemove(trait, (entity) => {
                    // Remove hooks run before the bit is cleared, so this is the
                    // transition away from having every constituent.
                    if (hasAspect(world, entity, aspect)) callback(entity);
                })
            );
        } else {
            unsubs.push(
                world.onChange(trait, (entity) => {
                    if (hasAspect(world, entity, aspect)) callback(entity);
                })
            );
        }
    }

    return () => {
        for (let i = 0; i < unsubs.length; i++) unsubs[i]();
    };
}

import { $internal, type Brand } from '../common';
import type { Entity } from '../entity/types';
import { isRelation } from '../relation/utils/is-relation';
import { addTrait, getTrait, hasTrait, removeTrait, setTrait, trait } from '../trait/trait';
import type { ConfigurableTrait, Trait } from '../trait/types';
import { universe } from '../universe/universe';
import type { World } from '../world';
import { getTraitAspects, registerAspect } from './registry';
import { $aspect } from './symbols';
import type { Aspect, AspectInput, AspectValue, FlattenAspectInputs } from './types';

let aspectId = 0;

export /* @inline @pure */ function isAspect(value: unknown): value is Aspect {
    return (value as Brand<typeof $aspect> | null | undefined)?.[$aspect] as unknown as boolean;
}

function getTraitFields(t: Trait): string[] {
    const ctx = t[$internal];
    if (ctx.type === 'tag') return [];
    if (ctx.type === 'aos') return Object.keys((t.schema as () => object)() ?? {});
    return Object.keys(t.schema);
}

export function createAspect<T extends AspectInput[]>(...inputs: T): Aspect<FlattenAspectInputs<T>> {
    const traits: Trait[] = [];

    for (const input of inputs) {
        if (isAspect(input)) {
            for (const t of input.traits) if (!traits.includes(t)) traits.push(t);
            continue;
        }
        if (isRelation(input) || (input as Trait)[$internal]?.relation) {
            throw new Error('Koota: Relations cannot be used as aspect constituents.');
        }
        if (!traits.includes(input as Trait)) traits.push(input as Trait);
    }

    if (traits.length < 2) {
        throw new Error('Koota: An aspect requires at least two traits.');
    }

    const fields = new Map<Trait, string[]>();
    const owners = new Map<string, Trait>();
    const schema: Record<string, unknown> = {};
    const dataTraits: Trait[] = [];

    for (const t of traits) {
        const traitFields = getTraitFields(t);
        for (const field of traitFields) {
            if (owners.has(field)) {
                throw new Error(`Koota: Aspect constituents have overlapping field "${field}".`);
            }
            owners.set(field, t);
        }
        fields.set(t, traitFields);
        if (t[$internal].type !== 'tag') dataTraits.push(t);

        if (t[$internal].type === 'soa') Object.assign(schema, t.schema);
        else if (t[$internal].type === 'aos') {
            for (const field of traitFields) schema[field] = (t.schema as () => any)()[field];
        }
    }

    const id = aspectId++;
    const aspect = Object.assign((params?: AspectValue<Aspect>) => [aspect, params ?? {}], {
        [$aspect]: true,
        [$internal]: { marker: trait(), fields, dataTraits },
    }) as unknown as Aspect<FlattenAspectInputs<T>>;

    Object.defineProperty(aspect, 'id', { value: id, enumerable: true });
    Object.defineProperty(aspect, 'traits', { value: Object.freeze(traits), enumerable: true });
    Object.defineProperty(aspect, 'schema', { value: Object.freeze(schema), enumerable: true });

    registerAspect(aspect);

    // Backfill markers for entities that already satisfy the aspect.
    for (const world of universe.worlds) {
        if (!world) continue;
        for (const entity of world.entities) {
            if (hasAllConstituents(world, entity, aspect)) {
                addTrait(world, entity, aspect[$internal].marker);
            }
        }
    }

    return aspect;
}

export function hasAllConstituents(world: World, entity: Entity, aspect: Aspect): boolean {
    const traits = aspect.traits;
    for (let i = 0; i < traits.length; i++) {
        if (!hasTrait(world, entity, traits[i])) return false;
    }
    return true;
}

export function syncAspectsOnAdd(world: World, entity: Entity, t: Trait) {
    const aspects = getTraitAspects(t);
    if (!aspects) return;
    for (let i = 0; i < aspects.length; i++) {
        const aspect = aspects[i];
        const marker = aspect[$internal].marker;
        if (!hasTrait(world, entity, marker) && hasAllConstituents(world, entity, aspect)) {
            addTrait(world, entity, marker);
        }
    }
}

export function syncAspectsOnRemove(world: World, entity: Entity, t: Trait) {
    const aspects = getTraitAspects(t);
    if (!aspects) return;
    for (let i = 0; i < aspects.length; i++) {
        const marker = aspects[i][$internal].marker;
        if (hasTrait(world, entity, marker)) removeTrait(world, entity, marker);
    }
}

export function pickFields(aspect: Aspect, t: Trait, value: Record<string, any>) {
    const traitFields = aspect[$internal].fields.get(t)!;
    let picked: Record<string, any> | undefined;
    for (const field of traitFields) {
        if (field in value) (picked ??= {})[field] = value[field];
    }
    return picked;
}

export function mergeAspectRecord(records: unknown[]) {
    const merged: Record<string, any> = {};
    for (let i = 0; i < records.length; i++) Object.assign(merged, records[i] as object);
    return merged;
}

export function expandAspectConfig(
    world: World,
    entity: Entity,
    aspect: Aspect,
    params: Record<string, any> | undefined
): ConfigurableTrait[] {
    const configs: ConfigurableTrait[] = [];
    for (const t of aspect.traits) {
        if (hasTrait(world, entity, t)) continue;
        const picked = params ? pickFields(aspect, t, params) : undefined;
        if (!picked) {
            configs.push(t);
        } else if (t[$internal].type === 'aos') {
            configs.push([t, Object.assign((t.schema as () => any)(), picked)] as ConfigurableTrait);
        } else {
            configs.push([t, picked] as ConfigurableTrait);
        }
    }
    return configs;
}

export function getAspect(
    world: World,
    entity: Entity,
    aspect: Aspect
): Record<string, any> | undefined {
    if (!hasAllConstituents(world, entity, aspect)) return undefined;
    const dataTraits = aspect[$internal].dataTraits;
    const records: unknown[] = dataTraits.map((t) => getTrait(world, entity, t));
    return mergeAspectRecord(records);
}

export function setAspect(
    world: World,
    entity: Entity,
    aspect: Aspect,
    value: any,
    triggerChanged = true
) {
    if (typeof value === 'function') {
        const prev = getAspect(world, entity, aspect);
        if (!prev) return;
        value = value(prev);
    }
    if (!value) return;

    for (const t of aspect[$internal].dataTraits) {
        if (!hasTrait(world, entity, t)) continue;
        const picked = pickFields(aspect, t, value);
        if (!picked) continue;

        if (t[$internal].type === 'aos') {
            const current = getTrait(world, entity, t);
            setTrait(world, entity, t, Object.assign(current ?? {}, picked), triggerChanged);
        } else {
            setTrait(world, entity, t, picked, triggerChanged);
        }
    }
}

export function removeAspect(world: World, entity: Entity, aspect: Aspect) {
    removeTrait(world, entity, ...aspect.traits);
}

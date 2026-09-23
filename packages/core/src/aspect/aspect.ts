import { $internal } from '../common';
import type { Entity } from '../entity/types';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import type { Trait } from '../trait/types';

/** Preserve concrete trait tuples when modifiers are built from trait arguments. */
export type FlattenInputs<T> = T extends readonly [infer First, ...infer Rest]
    ? First extends Aspect
        ? [...First['traits'], ...FlattenInputs<Rest>]
        : First extends Relation<infer R>
          ? [R, ...FlattenInputs<Rest>]
          : First extends Trait
            ? [First, ...FlattenInputs<Rest>]
            : FlattenInputs<Rest>
    : [];

export const $aspect = Symbol.for('koota.aspect');

export type AspectSubscriber = (entity: Entity) => void;

export type Aspect = {
    readonly [$aspect]: true;
    /** Public read-only ID. Each createAspect call has its own id. */
    readonly id: number;
    /** Flattened constituent traits, in first-seen order. */
    readonly traits: readonly Trait[];
    /** Merged constituent schemas. Tag traits contribute no fields. */
    readonly schema: Readonly<Record<string, unknown>>;
    readonly [$internal]: {
        traits: Trait[];
        keysByTrait: Map<Trait, string[]>;
        addSubscriptions: Set<AspectSubscriber>;
        removeSubscriptions: Set<AspectSubscriber>;
        changeSubscriptions: Set<AspectSubscriber>;
    };
    (params?: Record<string, unknown>): [Aspect, Record<string, unknown>];
};

let aspectId = 0;

export function isAspect(value: unknown): value is Aspect {
    return (value as { [$aspect]?: true } | null | undefined)?.[$aspect] === true;
}

function flattenConstituent(input: unknown, into: Trait[]) {
    if (isRelation(input) || (input as Trait | undefined)?.[$internal]?.relation) {
        throw new Error('Koota: Aspects cannot include relations.');
    }

    if (isAspect(input)) {
        for (let i = 0; i < input.traits.length; i++) into.push(input.traits[i]);
        return;
    }

    const trait = input as Trait | undefined;
    if (!trait || typeof trait !== 'function' || !trait[$internal] || typeof trait.id !== 'number') {
        throw new Error('Koota: createAspect received an invalid constituent.');
    }

    if (trait[$internal].type === 'aos') {
        throw new Error('Koota: Aspects cannot include AoS traits.');
    }

    into.push(trait);
}

/**
 * Group two or more traits (or nested aspects) into one addressable unit.
 * Field names must be unique across constituents. Relations are rejected.
 */
export function createAspect(...inputs: unknown[]): Aspect {
    if (inputs.length < 2) {
        throw new Error('Koota: createAspect requires at least two constituents.');
    }

    const traits: Trait[] = [];
    for (let i = 0; i < inputs.length; i++) flattenConstituent(inputs[i], traits);

    const schema: Record<string, unknown> = {};
    const keysByTrait = new Map<Trait, string[]>();

    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        const traitSchema = trait.schema as Record<string, unknown>;
        const keys = Object.keys(traitSchema);
        const owned: string[] = [];

        for (let k = 0; k < keys.length; k++) {
            const key = keys[k];
            if (Object.prototype.hasOwnProperty.call(schema, key)) {
                throw new Error(
                    `Koota: Aspect field "${key}" is declared on multiple constituents.`
                );
            }
            schema[key] = traitSchema[key];
            owned.push(key);
        }

        keysByTrait.set(trait, owned);
    }

    const id = aspectId++;
    const internal = {
        traits,
        keysByTrait,
        addSubscriptions: new Set<AspectSubscriber>(),
        removeSubscriptions: new Set<AspectSubscriber>(),
        changeSubscriptions: new Set<AspectSubscriber>(),
    };

    const aspect = Object.assign(
        (params?: Record<string, unknown>) => [aspect, params ?? {}],
        { [$aspect]: true as const, [$internal]: internal }
    ) as Aspect;

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
        value: Object.freeze(schema),
        writable: false,
        enumerable: true,
        configurable: false,
    });

    return aspect;
}

export type ModifierTerm =
    | { kind: 'trait'; trait: Trait }
    | { kind: 'aspect'; aspect: Aspect };

/**
 * Split modifier arguments into loose traits and aspect groups.
 * Relations are reduced to their underlying trait, matching existing modifiers.
 * `terms` preserves argument order for query result columns.
 */
export function splitModifierInputs(inputs: readonly unknown[]): {
    traits: Trait[];
    aspects: Aspect[];
    terms: ModifierTerm[];
} {
    const traits: Trait[] = [];
    const aspects: Aspect[] = [];
    const terms: ModifierTerm[] = [];

    for (let i = 0; i < inputs.length; i++) {
        const input = inputs[i];

        if (isRelation(input)) {
            const trait = input[$internal].trait;
            traits.push(trait);
            terms.push({ kind: 'trait', trait });
            continue;
        }

        if (isAspect(input)) {
            aspects.push(input);
            terms.push({ kind: 'aspect', aspect: input });
            for (let j = 0; j < input.traits.length; j++) traits.push(input.traits[j]);
            continue;
        }

        const trait = input as Trait;
        traits.push(trait);
        terms.push({ kind: 'trait', trait });
    }

    return { traits, aspects, terms };
}

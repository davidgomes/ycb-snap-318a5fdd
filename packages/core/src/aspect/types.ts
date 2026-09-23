import type { $internal } from '../common';
import type { Relation, RelationPair } from '../relation/types';
import type { ExtractSchema, SetTraitCallback, Trait, TraitRecord, TraitValue } from '../trait/types';
import type { $aspect } from './is-aspect';

export type AspectPart = {
    trait: Trait;
    keys: string[];
};

export type AspectInternal = {
    isAspect: true;
    completeness: Trait;
    parts: AspectPart[];
    fieldOwners: Map<string, Trait>;
};

type UnionToIntersection<U> = (U extends unknown ? (arg: U) => void : never) extends (
    arg: infer I
) => void
    ? I
    : never;

type DataTraitRecord<T> = T extends Trait
    ? T extends { [$internal]: { type: 'tag' } }
        ? {}
        : TraitRecord<T>
    : {};

export type AspectRecord<T extends readonly Trait[]> = UnionToIntersection<
    DataTraitRecord<T[number]>
>;

type SchemaOf<T> =
    T extends Trait<infer S> ? (S extends (...args: never[]) => unknown ? object : S) : object;

type DistributeSchema<T> = T extends Trait
    ? T extends { [$internal]: { type: 'tag' } }
        ? {}
        : SchemaOf<T>
    : {};

export type AspectSchema<T extends readonly Trait[]> = UnionToIntersection<
    DistributeSchema<T[number]>
>;

export type AspectValue<T extends readonly Trait[]> =
    | Partial<AspectRecord<T>>
    | ((prev: AspectRecord<T>) => Partial<AspectRecord<T>>);

export type AspectInit<A extends Aspect = Aspect> = [A, Partial<AspectRecord<A['traits']>>];

export type SetValue<T> =
    T extends Aspect<infer Traits>
        ? AspectValue<Traits>
        : T extends Trait | RelationPair
          ? TraitValue<ExtractSchema<T>> | SetTraitCallback<T>
          : never;

export type GetValue<T> =
    T extends Aspect<infer Traits>
        ? AspectRecord<Traits> | undefined
        : T extends Trait | RelationPair
          ? TraitRecord<ExtractSchema<T>> | undefined
          : undefined;

export type Aspect<T extends readonly Trait[] = readonly Trait[]> = {
    readonly [$aspect]: true;
    readonly id: number;
    readonly traits: T;
    readonly schema: AspectSchema<T>;
    [$internal]: AspectInternal;
} & ((params?: Partial<AspectRecord<T>>) => [Aspect<T>, Partial<AspectRecord<T>>]);

export type TraitsOf<T> =
    T extends Aspect<infer Inner>
        ? Inner extends readonly Trait[]
            ? [...Inner]
            : []
        : T extends Trait
          ? [T]
          : [];

export type FlattenList<T extends readonly (Trait | Aspect)[]> = T extends readonly [
    infer Head,
    ...infer Tail,
]
    ? Tail extends readonly (Trait | Aspect)[]
        ? [...TraitsOf<Head>, ...FlattenList<Tail>]
        : TraitsOf<Head>
    : [];

export type NormalizeModifierSource<T> = T extends Aspect
    ? T
    : T extends Relation<infer R>
      ? R
      : T extends Trait
        ? T
        : never;

export type NormalizeModifierSources<T extends readonly unknown[]> = {
    [K in keyof T]: NormalizeModifierSource<T[K]>;
};

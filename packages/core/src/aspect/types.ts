import type { $internal } from '../common';
import type { AoSFactory } from '../storage';
import type { IsTag, Trait, TraitRecord } from '../trait/types';
import type { $aspect } from './symbols';

type Simplify<T> = { [K in keyof T]: T[K] } & {};

export type AspectInput = Trait | Aspect;

/** Flattens a tuple of traits and aspects into a tuple of their constituent traits. */
export type FlattenAspectInputs<T extends AspectInput[]> = T extends [
    infer First,
    ...infer Rest extends AspectInput[],
]
    ? [...(First extends Aspect<infer U> ? U : [First]), ...FlattenAspectInputs<Rest>]
    : T extends []
      ? []
      : Trait[];

type MergeTraitRecords<T extends Trait[]> = T extends [
    infer First extends Trait,
    ...infer Rest extends Trait[],
]
    ? (IsTag<First> extends true ? {} : TraitRecord<First>) & MergeTraitRecords<Rest>
    : T extends []
      ? {}
      : Record<string, any>;

type MergeTraitSchemas<T extends Trait[]> = T extends [
    infer First extends Trait,
    ...infer Rest extends Trait[],
]
    ? (IsTag<First> extends true
          ? {}
          : First['schema'] extends AoSFactory
            ? TraitRecord<First>
            : First['schema']) &
          MergeTraitSchemas<Rest>
    : T extends []
      ? {}
      : Record<string, any>;

export type AspectRecordFromTraits<T extends Trait[]> = Simplify<MergeTraitRecords<T>>;
export type AspectValueFromTraits<T extends Trait[]> = Partial<AspectRecordFromTraits<T>>;
export type AspectSchemaFromTraits<T extends Trait[]> = Simplify<MergeTraitSchemas<T>>;

export type ExtractAspectTraits<A extends Aspect> = A extends Aspect<infer T> ? T : never;

/** The merged record of all constituent trait fields. */
export type AspectRecord<A extends Aspect> = AspectRecordFromTraits<ExtractAspectTraits<A>>;

/** A partial merged record, used for setting and initializing aspect fields. */
export type AspectValue<A extends Aspect> = AspectValueFromTraits<ExtractAspectTraits<A>>;

export type AspectTuple<A extends Aspect = Aspect> = [A, AspectValue<A>];

export type SetAspectCallback<A extends Aspect> = (prev: AspectRecord<A>) => AspectValue<A>;

export type AspectInternal = {
    /** Field keys owned by each constituent, aligned with `traits`. */
    fields: string[][];
};

export type Aspect<T extends Trait[] = Trait[]> = {
    readonly [$aspect]: true;
    /** Public read-only ID. Shares the trait ID space so it never collides with a trait. */
    readonly id: number;
    /** The flattened constituent traits. */
    readonly traits: T;
    /** The merged schema of all constituent traits. */
    readonly schema: AspectSchemaFromTraits<T>;
    [$internal]: AspectInternal;
} & ((params?: AspectValueFromTraits<T>) => [Aspect<T>, AspectValueFromTraits<T>]);

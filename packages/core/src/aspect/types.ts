import type { $internal } from '../common';
import type { AoSFactory } from '../storage';
import type { ExtractSchema, IsTag, Trait, TraitRecord } from '../trait/types';
import type { $aspect } from './symbols';

/** Anything that can be passed to `createAspect`. Nested aspects flatten to their traits. */
export type AspectInput = Trait | Aspect<any>;

type Simplify<T> = { [K in keyof T]: T[K] } & {};

type ConstituentRecord<T extends Trait> = IsTag<T> extends true ? {} : TraitRecord<T>;

type ConstituentSchema<T extends Trait> =
    IsTag<T> extends true
        ? {}
        : ExtractSchema<T> extends AoSFactory
          ? ReturnType<ExtractSchema<T>>
          : ExtractSchema<T>;

type MergeRecords<T extends readonly Trait[]> = T extends readonly [
    infer First extends Trait,
    ...infer Rest extends Trait[],
]
    ? ConstituentRecord<First> & MergeRecords<Rest>
    : T extends readonly []
      ? {}
      : Record<string, any>;

type MergeSchemas<T extends readonly Trait[]> = T extends readonly [
    infer First extends Trait,
    ...infer Rest extends Trait[],
]
    ? ConstituentSchema<First> & MergeSchemas<Rest>
    : T extends readonly []
      ? {}
      : Record<string, any>;

type AspectRecordFromTraits<T extends readonly Trait[]> = Simplify<MergeRecords<T>>;

/** The merged schema of every constituent of an aspect. */
export type AspectSchema<T extends Trait[] = Trait[]> = Simplify<MergeSchemas<T>>;

export type Aspect<T extends Trait[] = Trait[]> = {
    readonly [$aspect]: true;
    /** Public read-only ID, unique per `createAspect` call */
    readonly id: number;
    /** The flattened, deduplicated constituent traits */
    readonly traits: readonly [...T];
    readonly schema: AspectSchema<T>;
    [$internal]: {
        /** Field names owned by each constituent, parallel to `traits`. Tags own no fields. */
        fields: string[][];
        /** Constituents that hold data (SoA or AoS), in constituent order. */
        dataTraits: Trait[];
        /** Field names owned by each data trait, parallel to `dataTraits`. */
        dataFields: string[][];
    };
} & ((
    params?: Partial<AspectRecordFromTraits<T>>
) => [Aspect<T>, Partial<AspectRecordFromTraits<T>>]);

export type ExtractAspectTraits<A> = A extends Aspect<infer T> ? T : never;

/** The merged object of all constituent fields, as returned by `entity.get(aspect)`. */
export type AspectRecord<A extends Aspect<any>> = AspectRecordFromTraits<ExtractAspectTraits<A>>;

export type AspectValue<A extends Aspect<any>> = Partial<AspectRecord<A>>;

export type SetAspectCallback<A extends Aspect<any>> = (prev: AspectRecord<A>) => AspectValue<A>;

export type AspectTuple<A extends Aspect<any> = Aspect> = [A, AspectValue<A>];

export type ConfigurableAspect<A extends Aspect<any> = Aspect> = A | AspectTuple<A>;

/** Whether an aspect has any constituent that holds data. Tag-only aspects do not. */
export type AspectHasData<A extends Aspect<any>> = keyof AspectRecord<A> extends never ? false : true;

/** Keeps only the constituents that hold data. */
export type ExtractDataTraits<T extends readonly Trait[]> = T extends readonly [
    infer First extends Trait,
    ...infer Rest extends Trait[],
]
    ? IsTag<First> extends true
        ? ExtractDataTraits<Rest>
        : [First, ...ExtractDataTraits<Rest>]
    : [];

/** Flattens a list of traits and aspects into the list of their traits. */
export type FlattenAspectInputs<T extends readonly AspectInput[]> = T extends readonly [
    infer First,
    ...infer Rest extends AspectInput[],
]
    ? [...(First extends Aspect<infer U> ? U : [First]), ...FlattenAspectInputs<Rest>]
    : T extends readonly []
      ? []
      : Trait[];

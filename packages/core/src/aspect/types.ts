import type { $internal } from '../common';
import type { AoSFactory } from '../storage';
import type { IsTag, Trait, TraitRecord } from '../trait/types';
import type { $aspect } from './symbols';

type UnionToIntersection<U> = (U extends any ? (x: U) => void : never) extends (x: infer I) => void
    ? I
    : never;

type Simplify<T> = { [K in keyof T]: T[K] } & {};

type DataRecord<T> = T extends Trait ? (IsTag<T> extends true ? never : TraitRecord<T>) : never;

type DataSchema<T> =
    T extends Trait<infer S>
        ? IsTag<T> extends true
            ? never
            : S extends AoSFactory
              ? ReturnType<S>
              : S
        : never;

type Merge<U> = [U] extends [never] ? {} : Simplify<UnionToIntersection<U>>;

/** The merged record of all constituent trait records of an aspect. */
export type AspectRecord<T extends Aspect<any> | Trait[]> =
    T extends Aspect<infer C> ? Merge<DataRecord<C[number]>> : T extends Trait[] ? Merge<DataRecord<T[number]>> : never;

export type AspectSchema<T extends Trait[]> = Merge<DataSchema<T[number]>>;

export type AspectValue<T extends Aspect<any> | Trait[]> = Partial<AspectRecord<T>>;

export type SetAspectCallback<T extends Aspect<any>> = (prev: AspectRecord<T>) => AspectValue<T>;

/** True when every constituent of the aspect is a tag, meaning it carries no data. */
export type IsTagAspect<T extends Aspect<any>> =
    T extends Aspect<infer C> ? ([DataRecord<C[number]>] extends [never] ? true : false) : false;

export type AspectInternal = {
    id: number;
    traits: Trait[];
    /** Field names owned by each constituent, aligned with `traits`. */
    fields: string[][];
    /** Maps a field name to the index of its owning constituent. */
    owners: Map<string, number>;
    /** Indices of constituents that store data (non-tags). */
    dataIndices: number[];
};

export type Aspect<T extends Trait[] = Trait[]> = {
    readonly [$aspect]: true;
    /** Public read-only ID, unique per `createAspect` call */
    readonly id: number;
    readonly traits: T;
    readonly schema: AspectSchema<T>;
    [$internal]: AspectInternal;
} & ((params?: AspectValue<T>) => [Aspect<T>, AspectValue<T>]);

export type AspectTuple<T extends Aspect<any> = Aspect<any>> = [T, AspectValue<T>];

export type AspectInput = Trait | Aspect<any>;

export type FlattenAspectInputs<T extends readonly AspectInput[]> = T extends readonly []
    ? []
    : T extends readonly [infer First, ...infer Rest]
    ? [
          ...(First extends Aspect<infer C> ? C : First extends Trait ? [First] : []),
          ...(Rest extends AspectInput[] ? FlattenAspectInputs<Rest> : []),
      ]
    : Trait[];

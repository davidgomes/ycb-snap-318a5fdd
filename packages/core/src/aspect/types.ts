import type { Trait, TraitRecord } from '../trait/types';
import type { $aspect } from './symbols';

type UnionToIntersection<U> = (U extends any ? (argument: U) => void : never) extends (
    argument: infer I
) => void
    ? I
    : never;

/** Tag schemas are `{}` or `{ [key: string]: never }`. Both must contribute no fields. */
type IsEmptySchema<S> = string extends keyof S ? true : keyof S extends never ? true : false;

/** Data-bearing schemas only. Tag traits contribute no fields. */
type SchemaOf<T> = T extends Trait<infer S>
    ? IsEmptySchema<S> extends true
        ? {}
        : S extends Record<string, any>
          ? S
          : {}
    : {};

type RecordOf<T> = T extends Trait
    ? IsEmptySchema<T['schema']> extends true
        ? {}
        : TraitRecord<T>
    : {};

/** Field defaults from every constituent, merged into one schema object. */
export type AspectSchema<T extends readonly Trait[]> = UnionToIntersection<SchemaOf<T[number]>>;

/** Runtime record returned by `get` and delivered to query readers. */
export type MergeTraits<T extends readonly Trait[]> = UnionToIntersection<RecordOf<T[number]>>;

export type Aspect<TTraits extends readonly Trait[] = readonly Trait[]> = {
    readonly [$aspect]: true;
    readonly id: number;
    readonly traits: TTraits;
    readonly schema: AspectSchema<TTraits>;
    (
        params?: Partial<MergeTraits<TTraits>>
    ): [Aspect<TTraits>, Partial<MergeTraits<TTraits>>];
};

export type AspectInput = Trait | Aspect;

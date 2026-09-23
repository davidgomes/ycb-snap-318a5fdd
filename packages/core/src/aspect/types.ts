import type { $aspect } from './symbols';
import type { Entity } from '../entity/types';
import type { AoSFactory, Schema } from '../storage';
import type { Trait } from '../trait/types';
import type { World } from '../world';

type UnionToIntersection<U> = (U extends unknown ? (value: U) => void : never) extends (
    value: infer I
) => void
    ? I
    : never;

type SchemaOf<T> = T extends Trait<infer S> ? S : never;

type AoSFields<S> = S extends AoSFactory
    ? ReturnType<S> extends Record<string, unknown>
        ? {
              [K in keyof ReturnType<S> as ReturnType<S>[K] extends (...args: never[]) => unknown
                  ? never
                  : K]: ReturnType<S>[K];
          }
        : Record<string, never>
    : never;

/** Distributes over a union of traits. Tag traits contribute no fields. */
type FieldsOf<T> = T extends unknown
    ? SchemaOf<T> extends AoSFactory
        ? AoSFields<SchemaOf<T>>
        : SchemaOf<T> extends Record<string, never>
          ? {}
          : SchemaOf<T>
    : never;

type PrimitiveField<T> = T extends number | bigint | string | boolean | null | undefined
    ? T
    : T extends (...args: never[]) => unknown
      ? T
      : never;

/** Merged constituent schema. Non-primitive AoS fields are omitted from the static schema. */
export type MergedSchema<T extends readonly Trait[]> =
    UnionToIntersection<FieldsOf<T[number]>> extends infer Merged
        ? {
              [K in keyof Merged as PrimitiveField<Merged[K]> extends never
                  ? never
                  : K]: PrimitiveField<Merged[K]>;
          } extends infer Safe
            ? Safe extends Schema
                ? Safe
                : Record<string, never>
            : Record<string, never>
        : Record<string, never>;

export type AspectInput = Trait;

export type FlattenAspectInputs<T extends readonly AspectInput[]> = T extends readonly [
    infer Head,
    ...infer Tail,
]
    ? Tail extends readonly AspectInput[]
        ? Head extends Aspect<infer Traits>
            ? [...Traits, ...FlattenAspectInputs<Tail>]
            : Head extends Trait
              ? [Head, ...FlattenAspectInputs<Tail>]
              : FlattenAspectInputs<Tail>
        : Head extends Aspect<infer Traits>
          ? Traits
          : Head extends Trait
            ? [Head]
            : []
    : [];

export type Aspect<TTraits extends readonly Trait[] = readonly Trait[]> = Trait<
    MergedSchema<TTraits>
> & {
    readonly traits: TTraits;
    readonly [$aspect]: true;
};

export type AspectOperations = {
    register(world: World, skip?: Entity): void;
    addTo(world: World, entity: Entity, params?: Record<string, unknown>): void;
    removeFrom(world: World, entity: Entity): void;
    commit(world: World, entity: Entity): void;
};

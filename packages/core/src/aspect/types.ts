import type { $internal } from '../common';
import type { ExtractIsTag, Trait, TraitRecord } from '../trait/types';
import type { $aspect } from './symbols';

type UnionToIntersection<U> = (U extends unknown ? (x: U) => void : never) extends (
    x: infer I
) => void
    ? I
    : never;

export type FlattenAspectInputs<T extends readonly unknown[]> = T extends [infer First, ...infer Rest]
    ? [
          ...(First extends Aspect<infer A> ? A : First extends Trait ? [First] : []),
          ...FlattenAspectInputs<Rest>,
      ]
    : [];

export type AspectRecord<T extends Aspect | Trait[]> =
    T extends { readonly [$aspect]: true; readonly traits: infer A extends Trait[] }
        ? AspectRecord<A>
        : T extends Trait[]
          ? UnionToIntersection<
                {
                    [K in keyof T]: T[K] extends Trait
                        ? ExtractIsTag<T[K]> extends true
                            ? object
                            : TraitRecord<T[K]>
                        : never;
                }[number]
            > extends infer R
              ? { [K in keyof R]: R[K] }
              : never
          : never;

export type AspectValue<T extends Aspect> = Partial<AspectRecord<T>>;

export type AspectInternal = {
    marker: Trait;
    /** Field names owned by each constituent (empty for tags) */
    fields: Map<Trait, string[]>;
    /** Constituents that carry data (non-tag), in order */
    dataTraits: Trait[];
};

export type Aspect<T extends Trait[] = any> = {
    readonly [$aspect]: true;
    readonly id: number;
    readonly traits: T;
    readonly schema: Record<string, unknown>;
    [$internal]: AspectInternal;
} & {
    // Method syntax keeps the params bivariant so specific aspects remain assignable to `Aspect`.
    call(params?: Partial<AspectRecord<T>>): [Aspect<T>, Partial<AspectRecord<T>>];
}['call'];

export type AspectInput = Trait | Aspect;

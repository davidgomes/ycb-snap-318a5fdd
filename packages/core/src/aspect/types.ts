import { Brand } from '../common';
import type { Trait } from '../trait/types';
import { $aspect } from './symbols';

export type AspectInternal = {
    /** Maps merged field names to the owning trait */
    fieldMap: ReadonlyMap<string, Trait>;
    /** Constituent traits that carry data (non-tag) */
    dataTraits: readonly Trait[];
};

export type Aspect<TSchema extends Record<string, unknown> = Record<string, unknown>> = {
    readonly [$aspect]: true;
    readonly id: number;
    readonly traits: readonly Trait[];
    readonly schema: TSchema;
    readonly [$internal]: AspectInternal;
};

export type AspectRecord<A extends Aspect> = A['schema'];

export type AspectInput = Trait | Aspect;
export type AspectTuple = [Aspect, Record<string, unknown>];
export type ConfigurableAspect = Aspect | AspectTuple;

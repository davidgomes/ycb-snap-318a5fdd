import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  Config,
  InferInput,
  InferOutput,
} from '../../types/index.ts';

/**
 * Recur symbol.
 */
export declare const RecurSymbol: unique symbol;

/**
 * Recur marker interface.
 *
 * Hint: This is the input and output type of `Recur`. It marks the positions
 * that `recursive` and `recursiveAsync` replace with the resolved type.
 */
export interface RecurMarker {
  readonly [RecurSymbol]: true;
}

/**
 * Recur config interface.
 *
 * @internal
 */
export interface RecurConfig extends Config<BaseIssue<unknown>> {
  /**
   * The schema that `Recur` refers to.
   *
   * @internal
   */
  readonly '~recur'?:
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>
    | undefined;
}

/**
 * Recur primitive type.
 */
type RecurPrimitive =
  | string
  | number
  | bigint
  | boolean
  | symbol
  | null
  | undefined;

/**
 * Strip recur type.
 *
 * Hint: This type replaces every `Recur` placeholder with `never`. Arrays,
 * maps and sets are constructed directly instead of using a mapped type, so
 * that TypeScript defers their item types and can handle recursive types.
 */
export type StripRecur<TValue> = 0 extends 1 & TValue
  ? TValue
  : unknown extends TValue
    ? TValue
    : TValue extends RecurMarker
      ? never
      : TValue extends RecurPrimitive | ((...args: never) => unknown)
        ? TValue
        : TValue extends Map<infer TKey, infer TItem>
          ? Map<StripRecur<TKey>, StripRecur<TItem>>
          : TValue extends ReadonlyMap<infer TKey, infer TItem>
            ? ReadonlyMap<StripRecur<TKey>, StripRecur<TItem>>
            : TValue extends Set<infer TItem>
              ? Set<StripRecur<TItem>>
              : TValue extends ReadonlySet<infer TItem>
                ? ReadonlySet<StripRecur<TItem>>
                : TValue extends readonly unknown[]
                  ? TValue[number][] extends TValue
                    ? TValue extends unknown[]
                      ? StripRecur<TValue[number]>[]
                      : readonly StripRecur<TValue[number]>[]
                    : { [TKey in keyof TValue]: StripRecur<TValue[TKey]> }
                  : { [TKey in keyof TValue]: StripRecur<TValue[TKey]> };

/**
 * Has recur type.
 *
 * Hint: A type contains a `Recur` placeholder if it is not assignable to the
 * same type with all placeholders replaced by `never`. Unlike a recursive
 * search, this relation check also terminates for recursive types.
 */
export type HasRecur<TValue> = [TValue] extends [StripRecur<TValue>]
  ? false
  : true;

/**
 * Resolve recur type.
 *
 * Hint: This type replaces every `Recur` placeholder with the resolved root
 * type, so that recursive positions reference the type itself. Parts of the
 * type that do not contain a placeholder are kept as they are.
 */
export type ResolveRecur<TValue, TRoot = TValue> = 0 extends 1 & TValue
  ? TValue
  : TValue extends RecurMarker
    ? RecurMarker extends TValue
      ? ResolveRecur<TRoot>
      : ResolveRecur<TRoot> &
          ResolveRecur<Omit<TValue, typeof RecurSymbol>, TRoot>
    : [TValue] extends [StripRecur<TValue>]
      ? TValue
      : TValue extends Map<infer TKey, infer TItem>
        ? Map<ResolveRecur<TKey, TRoot>, ResolveRecur<TItem, TRoot>>
        : TValue extends ReadonlyMap<infer TKey, infer TItem>
          ? ReadonlyMap<ResolveRecur<TKey, TRoot>, ResolveRecur<TItem, TRoot>>
          : TValue extends Set<infer TItem>
            ? Set<ResolveRecur<TItem, TRoot>>
            : TValue extends ReadonlySet<infer TItem>
              ? ReadonlySet<ResolveRecur<TItem, TRoot>>
              : TValue extends readonly unknown[]
                ? TValue[number][] extends TValue
                  ? TValue extends unknown[]
                    ? ResolveRecur<TValue[number], TRoot>[]
                    : readonly ResolveRecur<TValue[number], TRoot>[]
                  : {
                      [TKey in keyof TValue]: ResolveRecur<TValue[TKey], TRoot>;
                    }
                : { [TKey in keyof TValue]: ResolveRecur<TValue[TKey], TRoot> };

/**
 * Unresolved recur interface.
 */
export interface UnresolvedRecur {
  /**
   * The error message.
   */
  readonly '~error': 'Wrap the schema with recursive or recursiveAsync to resolve its Recur placeholders.';
}

/**
 * Recur state type.
 *
 * Hint: The state of unresolved schemas is `any`, because TypeScript ignores
 * `any` branches when computing the constraint of a deferred conditional type.
 * This way, schemas with generic types are still accepted.
 */
type RecurState<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> = TSchema extends unknown
  ? HasRecur<InferInput<TSchema>> extends true
    ? // eslint-disable-next-line @typescript-eslint/no-explicit-any
      any
    : HasRecur<InferOutput<TSchema>> extends true
      ? // eslint-disable-next-line @typescript-eslint/no-explicit-any
        any
      : 'resolved'
  : never;

/**
 * Recur state map type.
 */
type RecurStateMap = { readonly resolved: unknown } & {
  readonly [key: string]: UnresolvedRecur;
};

/**
 * Schema without recur type.
 *
 * Hint: This type rejects schemas whose input or output type still contains a
 * `Recur` placeholder, because they must be wrapped with `recursive` or
 * `recursiveAsync` first.
 */
export type SchemaWithoutRecur<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> = TSchema & RecurStateMap[RecurState<TSchema>];

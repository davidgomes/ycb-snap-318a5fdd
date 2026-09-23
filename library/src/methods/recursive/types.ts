import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  Config,
  InferInput,
  InferOutput,
  IsAny,
} from '../../types/index.ts';

/**
 * Recur placeholder interface.
 *
 * Hint: This type marks the self-referencing positions of a schema until they
 * are resolved by `recursive` or `recursiveAsync`.
 */
export interface RecurPlaceholder {
  /**
   * The placeholder brand.
   *
   * @internal
   */
  readonly '~recur': true;
}

/**
 * Recursive config interface.
 *
 * @internal
 */
export interface RecursiveConfig extends Config<BaseIssue<unknown>> {
  /**
   * The schema that resolves the recur placeholder.
   */
  readonly '~recur'?:
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>
    | undefined;
}

/**
 * Checks if a type was already visited.
 */
type IsVisited<TValue, TVisited> = true extends (
  TVisited extends unknown
    ? [TValue] extends [TVisited]
      ? [TVisited] extends [TValue]
        ? true
        : false
      : false
    : never
)
  ? true
  : false;

/**
 * Checks if a type contains the recur placeholder.
 *
 * Hint: Visited types are tracked so that already resolved self-referencing
 * types do not cause infinite recursion.
 */
type HasRecurPlaceholder<TValue, TVisited> = TValue extends RecurPlaceholder
  ? true
  : TValue extends
        | string
        | number
        | bigint
        | boolean
        | symbol
        | null
        | undefined
        | ((...args: never) => unknown)
    ? false
    : IsVisited<TValue, TVisited> extends true
      ? false
      : TValue extends ReadonlyMap<infer TKey, infer TItem>
        ? HasRecur<TKey, TVisited | TValue> | HasRecur<TItem, TVisited | TValue>
        : TValue extends ReadonlySet<infer TItem>
          ? HasRecur<TItem, TVisited | TValue>
          : TValue extends Promise<infer TItem>
            ? HasRecur<TItem, TVisited | TValue>
            : TValue extends readonly unknown[]
              ? HasRecur<TValue[number], TVisited | TValue>
              : {
                  [TKey in keyof TValue]-?: HasRecur<
                    TValue[TKey],
                    TVisited | TValue
                  >;
                }[keyof TValue];

/**
 * Checks if a type contains the recur placeholder.
 */
export type HasRecur<TValue, TVisited = never> =
  IsAny<TValue> extends true
    ? false
    : [TValue] extends [never]
      ? false
      : true extends HasRecurPlaceholder<TValue, TVisited>
        ? true
        : false;

/**
 * Replaces the recur placeholder with the root type.
 */
type ResolveRecurValue<TValue, TRoot> = TValue extends RecurPlaceholder
  ? ResolveRecur<TRoot, TRoot>
  : ResolveRecur<TValue, TRoot>;

/**
 * Replaces the nested recur placeholders with the root type.
 *
 * Hint: The root type is resolved with the same type arguments at each
 * placeholder, so the resulting type references itself. Maps, sets, promises
 * and arrays are written as explicit type references, because TypeScript only
 * defers their item types in that form. Otherwise, a root that is itself one
 * of these types would be instantiated infinitely.
 */
export type ResolveRecur<TValue, TRoot> =
  HasRecur<TValue> extends false
    ? TValue
    : TValue extends RecurPlaceholder
      ? never
      : TValue extends Map<infer TKey, infer TItem>
        ? Map<ResolveRecurValue<TKey, TRoot>, ResolveRecurValue<TItem, TRoot>>
        : TValue extends Set<infer TItem>
          ? Set<ResolveRecurValue<TItem, TRoot>>
          : TValue extends Promise<infer TItem>
            ? Promise<ResolveRecurValue<TItem, TRoot>>
            : TValue extends readonly unknown[]
              ? number extends TValue['length']
                ? TValue extends unknown[]
                  ? ResolveRecurValue<TValue[number], TRoot>[]
                  : readonly ResolveRecurValue<TValue[number], TRoot>[]
                : {
                    [TKey in keyof TValue]: ResolveRecurValue<
                      TValue[TKey],
                      TRoot
                    >;
                  }
              : {
                  [TKey in keyof TValue]: ResolveRecurValue<
                    TValue[TKey],
                    TRoot
                  >;
                };

/**
 * Infer recursive input type.
 */
export type InferRecursiveInput<
  TWrapped extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> = ResolveRecur<InferInput<TWrapped>, InferInput<TWrapped>>;

/**
 * Infer recursive output type.
 */
export type InferRecursiveOutput<
  TWrapped extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> = ResolveRecur<InferOutput<TWrapped>, InferOutput<TWrapped>>;

/**
 * Checks if a schema contains an unresolved recur placeholder in its input or
 * output type.
 */
export type HasUnresolvedRecur<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> =
  HasRecur<InferInput<TSchema>> extends true
    ? true
    : HasRecur<InferOutput<TSchema>>;

/**
 * Unresolved recur error interface.
 */
interface UnresolvedRecurError {
  /**
   * The error message.
   */
  readonly '~recur': 'Unresolved Recur placeholder. Wrap the schema with recursive(...) or recursiveAsync(...) first.';
}

/**
 * Resolved recur flag type.
 *
 * Hint: It is `never` if the schema contains an unresolved recur placeholder.
 * The first check evaluates to `unknown` when TypeScript substitutes generic
 * schemas with `any`, so that generic schemas are not rejected.
 */
type ResolvedRecurFlag<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> = [TSchema] extends [UnresolvedRecurError]
  ? unknown
  : HasUnresolvedRecur<TSchema> extends true
    ? never
    : unknown;

/**
 * Resolved recur schema type.
 *
 * Hint: This type rejects schemas with unresolved recur placeholders. Wrap
 * them with `recursive` or `recursiveAsync` first.
 */
export type ResolvedRecurSchema<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> = [ResolvedRecurFlag<TSchema>] extends [never]
  ? UnresolvedRecurError
  : TSchema;

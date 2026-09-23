import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  InferInput,
  InferOutput,
  IsAny,
} from '../../types/index.ts';

/**
 * Recur marker interface.
 */
export interface RecurMarker {
  /**
   * The recur brand.
   *
   * @internal
   */
  readonly '~recur': true;
}

/**
 * Builtin types that are never traversed.
 */
type Builtin =
  // eslint-disable-next-line @typescript-eslint/no-unsafe-function-type
  | Function
  | Date
  | RegExp
  | Blob
  | Promise<unknown>
  | string
  | number
  | bigint
  | boolean
  | symbol
  | null
  | undefined;

/**
 * Resolves the recursive type of a schema.
 */
export type ResolveRecur<
  TRoot extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
  TKind extends 'input' | 'output',
> = ReplaceRecur<
  TKind extends 'input' ? InferInput<TRoot> : InferOutput<TRoot>,
  TRoot,
  TKind
>;

/**
 * Replaces the recur marker with the resolved recursive type.
 */
export type ReplaceRecur<
  TValue,
  TRoot extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
  TKind extends 'input' | 'output',
> =
  IsAny<TValue> extends true
    ? TValue
    : TValue extends RecurMarker
      ? ResolveRecur<TRoot, TKind>
      : TValue extends Builtin
        ? TValue
        : TValue extends Map<infer TKey, infer TItem>
          ? Map<
              ReplaceRecur<TKey, TRoot, TKind>,
              ReplaceRecur<TItem, TRoot, TKind>
            >
          : TValue extends Set<infer TItem>
            ? Set<ReplaceRecur<TItem, TRoot, TKind>>
            : {
                [TKey in keyof TValue]: ReplaceRecur<
                  TValue[TKey],
                  TRoot,
                  TKind
                >;
              };

/**
 * Checks whether a type contains the recur marker.
 */
type ContainsRecurDeep<
  TValue,
  TDepth extends unknown[],
> = TDepth['length'] extends 10
  ? false
  : IsAny<TValue> extends true
    ? false
    : TValue extends RecurMarker
      ? true
      : TValue extends Builtin
        ? false
        : TValue extends Map<infer TKey, infer TItem>
          ? true extends
              | ContainsRecurDeep<TKey, [...TDepth, 0]>
              | ContainsRecurDeep<TItem, [...TDepth, 0]>
            ? true
            : false
          : TValue extends Set<infer TItem>
            ? ContainsRecurDeep<TItem, [...TDepth, 0]>
            : TValue extends object
              ? true extends {
                  [TKey in keyof TValue]-?: ContainsRecurDeep<
                    TValue[TKey],
                    [...TDepth, 0]
                  >;
                }[keyof TValue]
                ? true
                : false
              : false;

/**
 * Checks whether a type contains an unresolved recur marker.
 */
export type ContainsRecur<TValue> =
  true extends ContainsRecurDeep<TValue, []> ? true : false;

/**
 * Rejects schemas with unresolved recur placeholders.
 */
export type RejectRecur<TSchema> = TSchema extends {
  readonly '~types'?:
    | { readonly input: infer TInput; readonly output: infer TOutput }
    | undefined;
}
  ? ContainsRecur<TInput | TOutput> extends true
    ? never
    : unknown
  : unknown;

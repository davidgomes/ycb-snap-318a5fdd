import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  InferInput,
  InferOutput,
} from '../../types/index.ts';

/**
 * Recur symbol.
 */
export declare const RecurSymbol: unique symbol;

/**
 * Recur type interface.
 *
 * Hint: This type marks the positions of unresolved `Recur` placeholders in
 * the input and output types of a schema.
 */
export interface RecurType {
  readonly [RecurSymbol]: true;
}

/**
 * Has recur type.
 *
 * Hint: The depth is limited to prevent infinite recursion for recursive
 * types that do not contain `Recur` placeholders.
 */
type HasRecur<TValue, TDepth extends unknown[] = []> = TDepth extends {
  length: 20;
}
  ? false
  : 0 extends 1 & TValue
    ? false
    : TValue extends RecurType
      ? true
      : TValue extends object
        ? TValue extends (...args: never) => unknown
          ? false
          : TValue extends ReadonlyMap<infer TKey, infer TItem>
            ? HasRecur<TKey | TItem, [...TDepth, unknown]>
            : TValue extends ReadonlySet<infer TItem>
              ? HasRecur<TItem, [...TDepth, unknown]>
              : TValue extends readonly unknown[]
                ? HasRecur<TValue[number], [...TDepth, unknown]>
                : HasRecur<TValue[keyof TValue], [...TDepth, unknown]>
        : false;

/**
 * Contains recur type.
 *
 * Hint: The placeholder is considered present if it appears in either the
 * input or the output type of the schema.
 */
export type ContainsRecur<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> = true extends HasRecur<InferInput<TSchema>> | HasRecur<InferOutput<TSchema>>
  ? true
  : false;

/**
 * Without recur type.
 *
 * Hint: This type resolves to `unknown` if the schema does not contain any
 * unresolved `Recur` placeholders. Otherwise, it resolves to an object type
 * that explains the error, so that the schema is rejected when used as an
 * argument. The `any` branch is deliberate. TypeScript ignores it when it
 * computes the constraint of the deferred check type, so that generic
 * schemas are only related to the `unknown` branch.
 */
export type WithoutRecur<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> = [
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ContainsRecur<TSchema> extends true ? any : never,
] extends [never]
  ? unknown
  : {
      readonly '~error': 'Unresolved Recur placeholder. Wrap the schema with recursive(...) or recursiveAsync(...) first.';
    };

/**
 * Resolve recur type.
 *
 * Hint: Each `Recur` placeholder is replaced by the root type. The array, map
 * and set types are written explicitly so that TypeScript defers the
 * resolution of their type arguments, which keeps the resulting type
 * self-referencing.
 */
export type ResolveRecur<TValue, TRoot> = 0 extends 1 & TValue
  ? TValue
  : TValue extends RecurType
    ? ResolveRecur<TRoot, TRoot>
    : true extends HasRecur<TValue>
      ? TValue extends Map<infer TKey, infer TItem>
        ? Map<ResolveRecur<TKey, TRoot>, ResolveRecur<TItem, TRoot>>
        : TValue extends ReadonlyMap<infer TKey, infer TItem>
          ? ReadonlyMap<ResolveRecur<TKey, TRoot>, ResolveRecur<TItem, TRoot>>
          : TValue extends Set<infer TItem>
            ? Set<ResolveRecur<TItem, TRoot>>
            : TValue extends ReadonlySet<infer TItem>
              ? ReadonlySet<ResolveRecur<TItem, TRoot>>
              : TValue extends readonly unknown[]
                ? number extends TValue['length']
                  ? TValue extends unknown[]
                    ? ResolveRecur<TValue[number], TRoot>[]
                    : readonly ResolveRecur<TValue[number], TRoot>[]
                  : {
                      [TKey in keyof TValue]: ResolveRecur<TValue[TKey], TRoot>;
                    }
                : { [TKey in keyof TValue]: ResolveRecur<TValue[TKey], TRoot> }
      : TValue;

/**
 * Infer recursive input type.
 */
export type InferRecursiveInput<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> = ResolveRecur<InferInput<TSchema>, InferInput<TSchema>>;

/**
 * Infer recursive output type.
 */
export type InferRecursiveOutput<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> = ResolveRecur<InferOutput<TSchema>, InferOutput<TSchema>>;

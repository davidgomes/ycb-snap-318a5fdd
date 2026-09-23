import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  Config,
} from '../../types/index.ts';

/**
 * Config key that holds the active recursive schema stack.
 *
 * @internal
 */
export const recursiveStack: unique symbol = Symbol('recursive');

/**
 * Schema that can be resumed by `Recur`.
 */
type StackSchema =
  | BaseSchema<unknown, unknown, BaseIssue<unknown>>
  | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>;

/**
 * Parse config carrying the recursive schema stack.
 */
interface RecursiveConfig extends Config<BaseIssue<unknown>> {
  readonly [recursiveStack]?: readonly StackSchema[] | undefined;
}

/**
 * Returns the schema that `Recur` should delegate to.
 *
 * @param config The parse configuration.
 *
 * @returns The active schema, or `undefined` if `Recur` is unresolved.
 *
 * @internal
 */
export function getRecursiveSchema(
  config: Config<BaseIssue<unknown>>
): StackSchema | undefined {
  const stack = (config as RecursiveConfig)[recursiveStack];
  return stack?.[stack.length - 1];
}

/**
 * Pushes a schema onto the recursive stack for the duration of a parse.
 *
 * @param config The parse configuration.
 * @param schema The schema to resume when `Recur` is reached.
 *
 * @returns The configuration to pass to the wrapped schema.
 *
 * @internal
 */
export function pushRecursiveSchema(
  config: Config<BaseIssue<unknown>>,
  schema: StackSchema
): Config<BaseIssue<unknown>> {
  const stack = (config as RecursiveConfig)[recursiveStack];
  return {
    ...config,
    [recursiveStack]: stack ? [...stack, schema] : [schema],
  } as Config<BaseIssue<unknown>>;
}

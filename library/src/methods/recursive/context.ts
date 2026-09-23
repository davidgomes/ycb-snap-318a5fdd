import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  Config,
} from '../../types/index.ts';

/**
 * Schema that can be bound while resolving `Recur`.
 */
type RecursiveTarget =
  | BaseSchema<unknown, unknown, BaseIssue<unknown>>
  | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>;

/**
 * Parse configuration that carries the schema `Recur` delegates to.
 */
interface ConfigWithRecur extends Config<BaseIssue<unknown>> {
  /**
   * The schema bound for `Recur`.
   *
   * Hint: A string key is copied by object spread, so wrappers such as
   * `message` keep the binding intact.
   */
  readonly '~recursive'?: RecursiveTarget | undefined;
}

/**
 * Binds the schema that `Recur` delegates to.
 *
 * @param config The current parse configuration.
 * @param target The schema to parse when `Recur` is reached.
 *
 * @returns The configuration for the nested parse.
 *
 * @internal
 */
export function _bindRecur(
  config: Config<BaseIssue<unknown>>,
  target: RecursiveTarget
): Config<BaseIssue<unknown>> {
  const nextConfig: ConfigWithRecur = {
    ...config,
    '~recursive': target,
  };
  return nextConfig;
}

/**
 * Reads the schema bound for `Recur`.
 *
 * @param config The current parse configuration.
 *
 * @returns The bound schema, if one is active.
 *
 * @internal
 */
export function _readRecur(
  config: Config<BaseIssue<unknown>>
): RecursiveTarget | undefined {
  return (config as ConfigWithRecur)['~recursive'];
}

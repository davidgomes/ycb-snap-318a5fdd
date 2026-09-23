import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  Config,
} from '../../types/index.ts';

/**
 * Config slot for the schema currently bound to `Recur`.
 */
const RECUR_TARGET: unique symbol = Symbol('recur');

/**
 * Schema that `Recur` can delegate to.
 */
type RecurTarget =
  | BaseSchema<unknown, unknown, BaseIssue<unknown>>
  | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>;

/**
 * Parse config carrying the schema bound to `Recur`.
 */
interface ConfigWithRecurTarget extends Config<BaseIssue<unknown>> {
  readonly [RECUR_TARGET]?: RecurTarget | undefined;
}

/**
 * Returns a config that resolves `Recur` to the given schema.
 *
 * Hint: The config object is copied so concurrent async parses keep independent
 * bindings. Nested schemas receive this copy because they forward `config`.
 *
 * @param config The parse configuration.
 * @param schema The schema bound to `Recur`.
 *
 * @returns The configuration with the bound schema.
 *
 * @internal
 */
// @__NO_SIDE_EFFECTS__
export function _withRecurTarget<
  const TConfig extends Config<BaseIssue<unknown>>,
>(config: TConfig, schema: RecurTarget): TConfig {
  return {
    ...config,
    [RECUR_TARGET]: schema,
  } as TConfig;
}

/**
 * Returns the schema bound to `Recur`, if the placeholder was wrapped.
 *
 * @param config The parse configuration.
 *
 * @returns The bound schema, or `undefined`.
 *
 * @internal
 */
// @__NO_SIDE_EFFECTS__
export function _getRecurTarget(
  config: Config<BaseIssue<unknown>>
): RecurTarget | undefined {
  return (config as ConfigWithRecurTarget)[RECUR_TARGET];
}

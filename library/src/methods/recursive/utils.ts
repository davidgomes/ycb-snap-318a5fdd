import type { BaseIssue, Config } from '../../types/index.ts';
import type { BaseSchema, BaseSchemaAsync } from '../../types/schema.ts';

/**
 * Recur root config key.
 */
const RECUR_ROOT: unique symbol = Symbol('valibot.recur');

/**
 * Recur root schema interface.
 */
export interface RecurRootSchema {
  /**
   * The wrapped schema.
   */
  readonly wrapped:
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>;
}

/**
 * Config with an optional recur root.
 */
interface ConfigWithRecurRoot<TIssue extends BaseIssue<unknown>>
  extends Config<TIssue> {
  readonly [RECUR_ROOT]?: RecurRootSchema | undefined;
}

/**
 * Returns the current recur root from the parse config.
 *
 * @param config The parse configuration.
 *
 * @returns The recur root schema or `undefined`.
 *
 * @internal
 */
// @__NO_SIDE_EFFECTS__
export function _getRecurRoot(
  config: Config<BaseIssue<unknown>>
): RecurRootSchema | undefined {
  return (config as ConfigWithRecurRoot<BaseIssue<unknown>>)[RECUR_ROOT];
}

/**
 * Returns a config that stores the current recur root.
 *
 * @param config The parse configuration.
 * @param root The recur root schema.
 *
 * @returns The config with a recur root.
 *
 * @internal
 */
// @__NO_SIDE_EFFECTS__
export function _withRecurRoot<TConfig extends Config<BaseIssue<unknown>>>(
  config: TConfig,
  root: RecurRootSchema
): TConfig {
  return { ...config, [RECUR_ROOT]: root };
}

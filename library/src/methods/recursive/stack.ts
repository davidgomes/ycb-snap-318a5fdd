import type { BaseIssue, Config, UnknownDataset } from '../../types/index.ts';

/**
 * Runnable schema stored while resolving `Recur`.
 */
interface RecurFrame {
  /**
   * Parses unknown input values.
   */
  readonly '~run': (
    dataset: UnknownDataset,
    config: Config<BaseIssue<unknown>>
  ) => unknown;
}

/**
 * Config object that may carry the active recursion stack.
 *
 * Hint: The stack is stored on an enumerable symbol so wrappers that clone a
 * config with object spread, such as `message` and `config`, keep the same
 * stack array.
 */
interface ConfigWithRecurStack {
  readonly [recurStackKey]?: RecurFrame[] | undefined;
}

/**
 * Symbol under which the active recursion stack is stored.
 */
const recurStackKey: unique symbol = Symbol('recurStack');

/**
 * Returns the recursion stack stored on a config, creating it when missing.
 *
 * @param config The parse configuration.
 *
 * @returns The recursion stack.
 *
 * @internal
 */
export function getRecurStack(config: object): RecurFrame[] {
  const record = config as ConfigWithRecurStack;
  const stack = record[recurStackKey];
  if (stack) {
    return stack;
  }
  const next: RecurFrame[] = [];
  (config as { [recurStackKey]: RecurFrame[] })[recurStackKey] = next;
  return next;
}

/**
 * Returns the schema currently bound to `Recur`.
 *
 * @param config The parse configuration.
 *
 * @returns The active recursive schema, if any.
 *
 * @internal
 */
// @__NO_SIDE_EFFECTS__
export function readRecur(config: object): RecurFrame | undefined {
  const stack = (config as ConfigWithRecurStack)[recurStackKey];
  return stack?.[stack.length - 1];
}

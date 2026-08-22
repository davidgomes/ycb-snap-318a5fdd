import type { Message } from "./message.ts";
import type { OptionName } from "./usage.ts";
import type { ValueParser } from "./valueparser.ts";
import { option, type OptionOptions } from "./primitives.ts";
import type { Parser, Mode } from "./parser.ts";

/** A single condition for a conditional option. */
export interface OptionCondition {
  readonly option: string;
  readonly value?: unknown;
}

/** A compound condition for a conditional option. */
export interface CompoundCondition {
  readonly anyOf?: readonly Condition[];
  readonly allOf?: readonly Condition[];
}

/** A condition accepted by conditional option helpers. */
export type Condition = string | OptionCondition | CompoundCondition;

/** Configuration for a conditional option dependency. */
export interface DependsOn extends CompoundCondition {
  readonly option?: string;
  readonly value?: unknown;
  readonly required?: boolean;
}

/** Options extended with conditional dependency metadata. */
export interface ConditionalOptionOptions extends OptionOptions {
  readonly dependsOn?: DependsOn;
}

/**
 * Creates an option that is required when its condition is satisfied.
 */
export function requiredWhen<M extends Mode, T>(
  condition: Condition | DependsOn,
  ...args: readonly [...readonly OptionName[], ValueParser<M, T>?]
): Parser<M, T | boolean, unknown> {
  return conditionalOption(condition, args, { required: true });
}

/**
 * Creates an option that is optional when its condition is not satisfied.
 */
export function optionalWhen<M extends Mode, T>(
  condition: Condition | DependsOn,
  ...args: readonly [...readonly OptionName[], ValueParser<M, T>?]
): Parser<M, T | boolean, unknown> {
  return conditionalOption(condition, args, { required: false });
}

/**
 * Creates an option whose visibility and requiredness depend on other options.
 */
export function conditionalOption<M extends Mode, T>(
  condition: Condition | DependsOn,
  flagSpec: readonly [...readonly OptionName[], ValueParser<M, T>?],
  required: { readonly required?: boolean } = {},
): Parser<M, T | boolean, unknown> {
  const dependsOn: DependsOn = typeof condition === "string"
    ? { option: condition, ...required }
    : { ...condition, ...required };
  return option(
    ...flagSpec,
    { dependsOn } as ConditionalOptionOptions,
  ) as Parser<M, T | boolean, unknown>;
}

export type { Message };

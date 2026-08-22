import {
  isDeferredParseState,
  isDependencySourceState,
  isPendingDependencySourceState,
} from "./dependency.ts";
import { type Message, message, optionName as eOptionName } from "./message.ts";
import type { Parser } from "./parser.ts";
import type { Usage, UsageTerm } from "./usage.ts";
import type { ValueParserResult } from "./valueparser.ts";

/**
 * A single option dependency condition.
 * @since 0.10.0
 */
export interface SingleOptionDependency {
  /**
   * The object key or CLI flag name of the option this depends on.
   */
  readonly option: string;

  /**
   * When set, the dependency is satisfied only when the referenced option
   * equals this value. When omitted, the referenced option must be truthy.
   */
  readonly value?: unknown;
}

/**
 * A compound option dependency condition using logical combinators.
 * @since 0.10.0
 */
export interface CompoundOptionDependency {
  /**
   * Satisfied when at least one nested condition is satisfied.
   * An empty array is treated as unsatisfied.
   */
  readonly anyOf?: readonly OptionDependencyCondition[];

  /**
   * Satisfied when every nested condition is satisfied.
   * An empty array is treated as satisfied.
   */
  readonly allOf?: readonly OptionDependencyCondition[];
}

/**
 * A condition that an option may depend on.
 * @since 0.10.0
 */
export type OptionDependencyCondition =
  | string
  | SingleOptionDependency
  | CompoundOptionDependency;

/**
 * Configuration for option dependencies.
 * @since 0.10.0
 */
export interface DependsOnConfig extends CompoundOptionDependency {
  /**
   * Shorthand for a single dependency on one option.
   */
  readonly option?: string;

  /**
   * Expected value for a single dependency. Omitted values require a truthy
   * referenced option.
   */
  readonly value?: unknown;

  /**
   * When `true`, using this option without a satisfied dependency produces a
   * validation error.
   */
  readonly required?: boolean;
}

/**
 * Context used to evaluate option dependencies within an {@link object} parser.
 * @since 0.10.0
 */
export interface OptionDependencyContext {
  /**
   * Parsed field states keyed by object field name.
   */
  readonly states: Readonly<Record<string | symbol, unknown>>;

  /**
   * Parsers keyed by object field name.
   */
  readonly parsers: Readonly<
    Record<string | symbol, Parser<"sync" | "async", unknown, unknown>>
  >;

  /**
   * Maps object field names and CLI flag names to field keys.
   */
  readonly referenceMap: ReadonlyMap<string, string | symbol>;
}

/**
 * Normalizes a dependency condition into a {@link DependsOnConfig}.
 *
 * @param condition The condition shorthand or configuration object.
 * @param defaults Default values merged into the result.
 * @returns A normalized dependency configuration.
 * @since 0.10.0
 */
export function normalizeDependsOn(
  condition: OptionDependencyCondition | DependsOnConfig,
  defaults: Pick<DependsOnConfig, "required"> = {},
): DependsOnConfig {
  if (typeof condition === "string") {
    return { option: condition, ...defaults };
  }

  if ("option" in condition && condition.option !== undefined) {
    return { ...condition, ...defaults };
  }

  if ("required" in condition || "anyOf" in condition || "allOf" in condition) {
    return { ...condition, ...defaults };
  }

  return { ...condition, ...defaults };
}

/**
 * Extracts a {@link DependsOnConfig} from parser usage metadata.
 *
 * @param usage Parser usage terms, including wrapped option usage.
 * @returns The dependency configuration, if present.
 * @since 0.10.0
 */
export function extractDependsOn(usage: Usage): DependsOnConfig | undefined {
  for (const term of usage) {
    const dependsOn = extractDependsOnFromTerm(term);
    if (dependsOn !== undefined) {
      return dependsOn;
    }
  }
  return undefined;
}

function extractDependsOnFromTerm(term: UsageTerm): DependsOnConfig | undefined {
  if (term.type === "option" && term.dependsOn !== undefined) {
    return term.dependsOn;
  }
  if (term.type === "optional" || term.type === "multiple") {
    return extractDependsOn(term.terms);
  }
  if (term.type === "exclusive") {
    for (const branch of term.terms) {
      const dependsOn = extractDependsOn(branch);
      if (dependsOn !== undefined) {
        return dependsOn;
      }
    }
  }
  return undefined;
}

/**
 * Builds a lookup map from object field keys and CLI flag names to field keys.
 *
 * @param parsers Parsers keyed by object field name.
 * @returns A map accepting either field keys or CLI flag names.
 * @since 0.10.0
 */
export function buildOptionReferenceMap(
  parsers: Readonly<
    Record<string | symbol, Parser<"sync" | "async", unknown, unknown>>
  >,
): Map<string, string | symbol> {
  const map = new Map<string, string | symbol>();

  for (const key of Reflect.ownKeys(parsers)) {
    const keyString = String(key);
    map.set(keyString, key);

    for (const name of extractAllOptionNames(parsers[key]!.usage)) {
      map.set(name, key);
    }
  }

  return map;
}

function extractAllOptionNames(usage: Usage): readonly string[] {
  const names: string[] = [];

  function traverse(terms: Usage): void {
    for (const term of terms) {
      if (term.type === "option") {
        names.push(...term.names);
      } else if (term.type === "optional" || term.type === "multiple") {
        traverse(term.terms);
      } else if (term.type === "exclusive") {
        for (const branch of term.terms) {
          traverse(branch);
        }
      }
    }
  }

  traverse(usage);
  return names;
}

/**
 * Returns the preferred user-facing CLI flag for an option reference.
 *
 * @param optionRef An object field key or CLI flag name.
 * @param context Dependency evaluation context.
 * @returns The preferred long flag name, if available.
 * @since 0.10.0
 */
export function getDependeeFlagName(
  optionRef: string,
  context: OptionDependencyContext,
): string {
  const key = resolveOptionKey(optionRef, context.referenceMap);
  if (key === undefined) {
    return optionRef.startsWith("-") ? optionRef : `--${optionRef}`;
  }

  const names = extractAllOptionNames(context.parsers[key]!.usage);
  const longName = names.find((name) => name.startsWith("--"));
  return longName ?? names[0] ?? optionRef;
}

function resolveOptionKey(
  optionRef: string,
  referenceMap: ReadonlyMap<string, string | symbol>,
): string | symbol | undefined {
  if (referenceMap.has(optionRef)) {
    return referenceMap.get(optionRef);
  }
  return undefined;
}

/**
 * Extracts the parsed value from an option field state.
 *
 * @param state The parser state for an option field.
 * @returns The parsed value, or `undefined` when unavailable.
 * @since 0.10.0
 */
export function extractOptionValue(state: unknown): unknown | undefined {
  if (state === undefined || state === null) {
    return undefined;
  }

  if (isPendingDependencySourceState(state)) {
    return undefined;
  }

  if (Array.isArray(state)) {
    if (state.length === 0) {
      return undefined;
    }
    return extractOptionValue(state[0]);
  }

  if (isDeferredParseState(state)) {
    return state.preliminaryResult.success
      ? state.preliminaryResult.value
      : undefined;
  }

  if (isDependencySourceState(state)) {
    return state.result.success ? state.result.value : undefined;
  }

  if (typeof state === "object" && "success" in state) {
    const result = state as ValueParserResult<unknown>;
    return result.success ? result.value : undefined;
  }

  return undefined;
}

/**
 * Determines whether an option was explicitly provided on the command line.
 *
 * @param state The parser state for an option field.
 * @param parser The option parser.
 * @returns `true` when the user explicitly provided the option.
 * @since 0.10.0
 */
export function wasOptionExplicitlyProvided(
  state: unknown,
  parser: Parser<"sync" | "async", unknown, unknown>,
): boolean {
  if (state === undefined || state === null) {
    return false;
  }

  if (isPendingDependencySourceState(state)) {
    return false;
  }

  if (Array.isArray(state)) {
    if (state.length === 0) {
      return false;
    }
    if (state[0] === undefined) {
      return false;
    }
    return wasOptionExplicitlyProvided(state[0], parser);
  }

  if (isDeferredParseState(state) || isDependencySourceState(state)) {
    return true;
  }

  if (typeof state === "object" && "success" in state) {
    const result = state as ValueParserResult<unknown>;
    if (!result.success) {
      return false;
    }

    const initialState = parser.initialState;
    if (
      typeof initialState === "object" &&
      initialState !== null &&
      "success" in initialState
    ) {
      const initialResult = initialState as ValueParserResult<unknown>;
      if (initialResult.success && initialResult.value === false) {
        return result.value === true;
      }
    }

    return true;
  }

  return false;
}

function isSingleConditionSatisfied(
  condition: SingleOptionDependency,
  context: OptionDependencyContext,
): boolean {
  const key = resolveOptionKey(condition.option, context.referenceMap);
  if (key === undefined) {
    return false;
  }

  const state = context.states[key];
  const value = extractOptionValue(state);

  if (value === undefined) {
    return false;
  }

  if ("value" in condition && condition.value !== undefined) {
    return value === condition.value;
  }

  return Boolean(value);
}

function isConditionSatisfied(
  condition: OptionDependencyCondition,
  context: OptionDependencyContext,
): boolean {
  if (typeof condition === "string") {
    return isSingleConditionSatisfied({ option: condition }, context);
  }

  if ("option" in condition && condition.option !== undefined) {
    return isSingleConditionSatisfied(
      condition as SingleOptionDependency,
      context,
    );
  }

  if ("allOf" in condition && condition.allOf !== undefined) {
    if (condition.allOf.length === 0) {
      return true;
    }
    return condition.allOf.every((nested) =>
      isConditionSatisfied(nested, context)
    );
  }

  if ("anyOf" in condition && condition.anyOf !== undefined) {
    if (condition.anyOf.length === 0) {
      return false;
    }
    return condition.anyOf.some((nested) =>
      isConditionSatisfied(nested, context)
    );
  }

  return false;
}

/**
 * Evaluates whether an option dependency configuration is satisfied.
 *
 * @param dependsOn The dependency configuration to evaluate.
 * @param context Dependency evaluation context.
 * @returns `true` when the dependency is satisfied.
 * @since 0.10.0
 */
export function isDependsOnSatisfied(
  dependsOn: DependsOnConfig,
  context: OptionDependencyContext,
): boolean {
  if (dependsOn.option !== undefined) {
    return isSingleConditionSatisfied(
      { option: dependsOn.option, value: dependsOn.value },
      context,
    );
  }

  if (dependsOn.allOf !== undefined) {
    if (dependsOn.allOf.length === 0) {
      return true;
    }
    return dependsOn.allOf.every((nested) =>
      isConditionSatisfied(nested, context)
    );
  }

  if (dependsOn.anyOf !== undefined) {
    if (dependsOn.anyOf.length === 0) {
      return false;
    }
    return dependsOn.anyOf.some((nested) =>
      isConditionSatisfied(nested, context)
    );
  }

  return false;
}

/**
 * Determines whether a dependee option was explicitly provided with a falsy
 * value.
 *
 * @param dependsOn The dependency configuration.
 * @param context Dependency evaluation context.
 * @returns `true` when any referenced dependee was explicitly falsy.
 * @since 0.10.0
 */
export function hasExplicitFalsyDependee(
  dependsOn: DependsOnConfig,
  context: OptionDependencyContext,
): boolean {
  return collectDependeeRefs(dependsOn).some((optionRef) => {
    const key = resolveOptionKey(optionRef, context.referenceMap);
    if (key === undefined) {
      return false;
    }

    const parser = context.parsers[key]!;
    const state = context.states[key];
    if (!wasOptionExplicitlyProvided(state, parser)) {
      return false;
    }

    const value = extractOptionValue(state);
    return !value;
  });
}

function collectDependeeRefs(dependsOn: DependsOnConfig): readonly string[] {
  if (dependsOn.option !== undefined) {
    return [dependsOn.option];
  }

  const refs: string[] = [];

  const walk = (condition: OptionDependencyCondition): void => {
    if (typeof condition === "string") {
      refs.push(condition);
      return;
    }
    if ("option" in condition && condition.option !== undefined) {
      refs.push(condition.option);
      return;
    }
    if ("allOf" in condition && condition.allOf !== undefined) {
      for (const nested of condition.allOf) {
        walk(nested);
      }
    }
    if ("anyOf" in condition && condition.anyOf) {
      for (const nested of condition.anyOf) {
        walk(nested);
      }
    }
  };

  if (dependsOn.allOf !== undefined) {
    for (const nested of dependsOn.allOf) {
      walk(nested);
    }
  }
  if (dependsOn.anyOf !== undefined) {
    for (const nested of dependsOn.anyOf) {
      walk(nested);
    }
  }

  return refs;
}

/**
 * Formats a dependency validation error for a required but unsatisfied option.
 *
 * @param dependeeFlag The user-facing CLI flag for the dependee option.
 * @param expectedValue The expected dependee value, if any.
 * @returns A formatted error message.
 * @since 0.10.0
 */
export function formatRequiresOptionError(
  dependeeFlag: string,
  expectedValue?: unknown,
): Message {
  if (expectedValue !== undefined) {
    return message`Option requires option ${eOptionName(dependeeFlag)} to be ${
      String(expectedValue)
    }.`;
  }
  return message`Option requires option ${eOptionName(dependeeFlag)}.`;
}

/**
 * Validates option dependencies for a field before completion.
 *
 * @param fieldKey The object field key being completed.
 * @param parser The parser for the field.
 * @param state The field state.
 * @param context Dependency evaluation context.
 * @returns An error message when validation fails, otherwise `undefined`.
 * @since 0.10.0
 */
export function validateOptionDependency(
  _fieldKey: string | symbol,
  parser: Parser<"sync" | "async", unknown, unknown>,
  state: unknown,
  context: OptionDependencyContext,
): Message | undefined {
  const dependsOn = extractDependsOn(parser.usage);
  if (dependsOn === undefined) {
    return undefined;
  }

  if (!wasOptionExplicitlyProvided(state, parser)) {
    return undefined;
  }

  const satisfied = isDependsOnSatisfied(dependsOn, context);

  if (dependsOn.required && !satisfied) {
    const dependeeRef = dependsOn.option ??
      collectDependeeRefs(dependsOn)[0] ??
      "option";
    const dependeeFlag = getDependeeFlagName(dependeeRef, context);
    return formatRequiresOptionError(dependeeFlag, dependsOn.value);
  }

  if (!satisfied && hasExplicitFalsyDependee(dependsOn, context)) {
    const dependeeRef = dependsOn.option ??
      collectDependeeRefs(dependsOn)[0] ??
      "option";
    const dependeeFlag = getDependeeFlagName(dependeeRef, context);
    return formatRequiresOptionError(dependeeFlag, dependsOn.value);
  }

  return undefined;
}

/**
 * Determines whether an option should be hidden from help and completion.
 *
 * @param parser The option parser.
 * @param context Dependency evaluation context.
 * @returns `true` when the option should be hidden.
 * @since 0.10.0
 */
export function isOptionDependencyHidden(
  parser: Parser<"sync" | "async", unknown, unknown>,
  context: OptionDependencyContext,
): boolean {
  const dependsOn = extractDependsOn(parser.usage);
  if (dependsOn === undefined) {
    return false;
  }

  if (dependsOn.required) {
    return false;
  }

  return !isDependsOnSatisfied(dependsOn, context);
}

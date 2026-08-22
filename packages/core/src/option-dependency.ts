import { isDeferredParseState, isDependencySourceState } from "./dependency.ts";
import { message, type Message } from "./message.ts";
import type { Parser } from "./parser.ts";
import type { Usage, UsageTerm } from "./usage.ts";
import type { ValueParserResult } from "./valueparser.ts";

/**
 * A single dependency condition referencing another option.
 * @since 0.11.0
 */
export interface DependsOnSingle {
  /** Object key or CLI flag name of the dependee option. */
  readonly option: string;
  /** When set, the dependee must equal this value. When omitted, the dependee must be truthy. */
  readonly value?: unknown;
}

/**
 * A compound dependency condition using logical operators.
 * @since 0.11.0
 */
export interface DependsOnCompound {
  readonly anyOf?: readonly DependsOnCondition[];
  readonly allOf?: readonly DependsOnCondition[];
}

/**
 * A dependency condition: option name, single condition, or compound condition.
 * @since 0.11.0
 */
export type DependsOnCondition =
  | string
  | DependsOnSingle
  | DependsOnCompound;

/**
 * Configuration for option dependencies.
 * @since 0.11.0
 */
export interface DependsOnConfig {
  readonly option?: string;
  readonly value?: unknown;
  readonly anyOf?: readonly DependsOnCondition[];
  readonly allOf?: readonly DependsOnCondition[];
  /**
   * When `true`, an unsatisfied dependency causes a validation error
   * when the dependent option is used.
   */
  readonly required?: boolean;
}

/**
 * Context for evaluating option dependencies within an {@link object} parser.
 * @since 0.11.0
 */
export interface ObjectFieldContext {
  /** The object field key of the parser being evaluated. */
  readonly fieldKey: string | symbol;
  /** Parsed states for all sibling fields in the object. */
  readonly siblingStates: Readonly<Record<string | symbol, unknown>>;
  /** Maps CLI flag names to object field keys. */
  readonly flagToKey: ReadonlyMap<string, string | symbol>;
  /** Maps object field keys to a primary user-facing CLI flag name. */
  readonly keyToPrimaryFlag: ReadonlyMap<string | symbol, string>;
  /** Parsers for all object fields, keyed by field name. */
  readonly parsers: Readonly<
    Record<string | symbol, Parser<"sync" | "async", unknown, unknown>>
  >;
}

/**
 * Normalizes a condition argument into a {@link DependsOnConfig}.
 * @since 0.11.0
 */
export function normalizeDependsOnConfig(
  condition: DependsOnCondition | DependsOnConfig,
  required?: boolean,
): DependsOnConfig {
  if (typeof condition === "string") {
    return { option: condition, ...(required !== undefined && { required }) };
  }
  if ("anyOf" in condition || "allOf" in condition) {
    const compound = condition as DependsOnCompound;
    return {
      ...(compound.anyOf !== undefined && { anyOf: compound.anyOf }),
      ...(compound.allOf !== undefined && { allOf: compound.allOf }),
      ...(required !== undefined && { required }),
      ...("required" in condition &&
        (condition as DependsOnConfig).required !== undefined &&
        { required: (condition as DependsOnConfig).required }),
    };
  }
  if ("option" in condition && typeof condition.option === "string") {
    const single = condition as DependsOnSingle;
    const config: DependsOnConfig = {
      option: single.option,
      ...(single.value !== undefined && { value: single.value }),
    };
    if ("required" in condition &&
      (condition as DependsOnConfig).required !== undefined) {
      return {
        ...config,
        required: (condition as DependsOnConfig).required,
      };
    }
    if (required !== undefined) {
      return { ...config, required };
    }
    return config;
  }
  const full = condition as DependsOnConfig;
  return {
    ...full,
    ...(required !== undefined && { required }),
  };
}

/**
 * Extracts {@link DependsOnConfig} from a parser's usage terms, including
 * wrapped optional/multiple terms.
 * @since 0.11.0
 */
export function extractDependsOnFromUsage(
  usage: Usage,
): DependsOnConfig | undefined {
  for (const term of usage) {
    const found = extractDependsOnFromTerm(term);
    if (found) return found;
  }
  return undefined;
}

function extractDependsOnFromTerm(term: UsageTerm): DependsOnConfig | undefined {
  if (term.type === "option" && term.dependsOn) {
    return term.dependsOn;
  }
  if (term.type === "optional" || term.type === "multiple") {
    return extractDependsOnFromUsage(term.terms);
  }
  if (term.type === "exclusive") {
    for (const branch of term.terms) {
      const found = extractDependsOnFromUsage(branch);
      if (found) return found;
    }
  }
  return undefined;
}

/**
 * Builds flag-to-key and key-to-flag maps from object parser field pairs.
 * @since 0.11.0
 */
export function buildOptionKeyMaps(
  parserPairs: readonly [string | symbol, Parser<"sync" | "async", unknown, unknown>][],
): {
  readonly flagToKey: ReadonlyMap<string, string | symbol>;
  readonly keyToPrimaryFlag: ReadonlyMap<string | symbol, string>;
} {
  const flagToKey = new Map<string, string | symbol>();
  const keyToPrimaryFlag = new Map<string | symbol, string>();

  for (const [key, parser] of parserPairs) {
    const names = collectOptionNamesFromUsage(parser.usage);
    if (names.length === 0) continue;
    const primary = names.find((n) => n.startsWith("--")) ?? names[0];
    keyToPrimaryFlag.set(key, primary);
    for (const name of names) {
      flagToKey.set(name, key);
    }
  }

  return { flagToKey, keyToPrimaryFlag };
}

function collectOptionNamesFromUsage(usage: Usage): string[] {
  const names: string[] = [];
  function traverse(terms: Usage): void {
    for (const term of terms) {
      if (term.type === "option") {
        names.push(...term.names);
      } else if (term.type === "optional" || term.type === "multiple") {
        traverse(term.terms);
      } else if (term.type === "exclusive") {
        for (const branch of term.terms) traverse(branch);
      }
    }
  }
  traverse(usage);
  return names;
}

/**
 * Resolves a dependency option reference to an object field key.
 * @since 0.11.0
 */
export function resolveDependeeKey(
  optionRef: string,
  objectKeys: readonly (string | symbol)[],
  flagToKey: ReadonlyMap<string, string | symbol>,
): string | symbol | undefined {
  if (objectKeys.some((k) => String(k) === optionRef)) {
    return objectKeys.find((k) => String(k) === optionRef);
  }
  return flagToKey.get(optionRef);
}

function unwrapParserState(state: unknown): unknown {
  if (state === undefined) return undefined;
  if (Array.isArray(state)) {
    if (state.length === 0) return undefined;
    return unwrapParserState(state[0]);
  }
  if (isDeferredParseState(state)) {
    return state.preliminaryResult;
  }
  if (isDependencySourceState(state)) {
    return state.result;
  }
  return state;
}

/**
 * Returns whether an option was explicitly provided by the user (as opposed
 * to using an implicit default such as an omitted boolean flag).
 * @since 0.11.0
 */
export function isOptionExplicitlyProvided(
  state: unknown,
  initialState: unknown,
): boolean {
  if (state === undefined) return false;
  if (Array.isArray(state)) {
    if (state.length === 0) return false;
    if (state.length === 1 && state[0] === undefined) return false;
    return isOptionExplicitlyProvided(state[0], initialState);
  }
  const unwrapped = unwrapParserState(state);
  if (
    unwrapped != null && typeof unwrapped === "object" &&
    "success" in unwrapped
  ) {
    const result = unwrapped as ValueParserResult<unknown>;
    if (!result.success) return false;
    const initialUnwrapped = unwrapParserState(initialState);
    if (
      initialUnwrapped != null && typeof initialUnwrapped === "object" &&
      "success" in initialUnwrapped
    ) {
      const initialResult = initialUnwrapped as ValueParserResult<unknown>;
      if (initialResult.success && result.success) {
        return JSON.stringify(result.value) !== JSON.stringify(initialResult.value);
      }
    }
    return true;
  }
  return state !== initialState;
}

/**
 * Extracts the completed value for a dependee option from sibling state.
 * Guards against calling complete on undefined inner parsers.
 * @since 0.11.0
 */
export function getDependeeValue(
  dependeeKey: string | symbol,
  context: ObjectFieldContext,
): { readonly value: unknown; readonly explicitlyProvided: boolean } {
  const parser = context.parsers[dependeeKey];
  if (parser == null) {
    return { value: undefined, explicitlyProvided: false };
  }

  const rawState = context.siblingStates[dependeeKey];
  const initialState = parser.initialState;
  const explicitlyProvided = isOptionExplicitlyProvided(rawState, initialState);

  if (rawState === undefined) {
    if (initialState === undefined) {
      return { value: undefined, explicitlyProvided: false };
    }
    const completed = parser.complete(initialState);
    if (completed instanceof Promise) {
      throw new TypeError(
        "Synchronous dependency evaluation cannot use async parsers.",
      );
    }
    if (completed.success) {
      return { value: completed.value, explicitlyProvided: false };
    }
    return { value: undefined, explicitlyProvided: false };
  }

  const unwrapped = unwrapParserState(rawState);
  if (
    unwrapped != null && typeof unwrapped === "object" && "success" in unwrapped
  ) {
    const result = unwrapped as ValueParserResult<unknown>;
    if (result.success) {
      return { value: result.value, explicitlyProvided };
    }
    return { value: undefined, explicitlyProvided };
  }

  const completed = parser.complete(rawState);
  if (completed instanceof Promise) {
    throw new TypeError(
      "Synchronous dependency evaluation cannot use async parsers.",
    );
  }
  if (completed.success) {
    return { value: completed.value, explicitlyProvided };
  }
  return { value: undefined, explicitlyProvided };
}

function isDependencyValueTruthy(value: unknown): boolean {
  if (value === false || value === 0 || value === "" || value == null) {
    return false;
  }
  if (value === "false" || value === "0" || value === "no") {
    return false;
  }
  return Boolean(value);
}

function isSingleConditionSatisfied(
  condition: DependsOnSingle,
  context: ObjectFieldContext,
  objectKeys: readonly (string | symbol)[],
): boolean {
  const dependeeKey = resolveDependeeKey(
    condition.option,
    objectKeys,
    context.flagToKey,
  );
  if (dependeeKey === undefined) return false;

  const { value } = getDependeeValue(dependeeKey, context);
  if (condition.value !== undefined) {
    return value === condition.value;
  }
  return isDependencyValueTruthy(value);
}

function isConditionSatisfied(
  condition: DependsOnCondition,
  context: ObjectFieldContext,
  objectKeys: readonly (string | symbol)[],
): boolean {
  if (typeof condition === "string") {
    return isSingleConditionSatisfied({ option: condition }, context, objectKeys);
  }
  if ("anyOf" in condition && condition.anyOf !== undefined) {
    if (condition.anyOf.length === 0) return false;
    return condition.anyOf.some((c) =>
      isConditionSatisfied(c, context, objectKeys)
    );
  }
  if ("allOf" in condition && condition.allOf !== undefined) {
    if (condition.allOf.length === 0) return true;
    return condition.allOf.every((c) =>
      isConditionSatisfied(c, context, objectKeys)
    );
  }
  if ("option" in condition && typeof condition.option === "string") {
    return isSingleConditionSatisfied(condition, context, objectKeys);
  }
  return false;
}

/**
 * Returns whether a dependency configuration is satisfied given sibling states.
 * @since 0.11.0
 */
export function isDependsOnSatisfied(
  config: DependsOnConfig,
  context: ObjectFieldContext,
): boolean {
  const objectKeys = Object.keys(context.parsers) as (string | symbol)[];
  for (const sym of Object.getOwnPropertySymbols(context.parsers)) {
    objectKeys.push(sym);
  }

  if (config.anyOf !== undefined) {
    if (config.anyOf.length === 0) return false;
    return config.anyOf.some((c) =>
      isConditionSatisfied(c, context, objectKeys)
    );
  }
  if (config.allOf !== undefined) {
    if (config.allOf.length === 0) return true;
    return config.allOf.every((c) =>
      isConditionSatisfied(c, context, objectKeys)
    );
  }
  if (config.option !== undefined) {
    return isSingleConditionSatisfied(
      { option: config.option, ...(config.value !== undefined && { value: config.value }) },
      context,
      objectKeys,
    );
  }
  return false;
}

/**
 * Returns whether any dependee referenced by the config was explicitly
 * provided with a falsy value.
 * @since 0.11.0
 */
export function hasExplicitFalsyDependee(
  config: DependsOnConfig,
  context: ObjectFieldContext,
): boolean {
  const objectKeys = Object.keys(context.parsers) as (string | symbol)[];
  for (const sym of Object.getOwnPropertySymbols(context.parsers)) {
    objectKeys.push(sym);
  }

  const checkSingle = (single: DependsOnSingle): boolean => {
    const dependeeKey = resolveDependeeKey(
      single.option,
      objectKeys,
      context.flagToKey,
    );
    if (dependeeKey === undefined) return false;
    const { value, explicitlyProvided } = getDependeeValue(dependeeKey, context);
    if (!explicitlyProvided) return false;
    if (single.value !== undefined) {
      return value === single.value && !isDependencyValueTruthy(value);
    }
    return !isDependencyValueTruthy(value);
  };

  const checkCondition = (condition: DependsOnCondition): boolean => {
    if (typeof condition === "string") {
      return checkSingle({ option: condition });
    }
    if ("anyOf" in condition && condition.anyOf !== undefined) {
      return condition.anyOf.some(checkCondition);
    }
    if ("allOf" in condition && condition.allOf !== undefined) {
      return condition.allOf.some(checkCondition);
    }
    if ("option" in condition && typeof condition.option === "string") {
      return checkSingle(condition);
    }
    return false;
  };

  if (config.option !== undefined) {
    return checkSingle({
      option: config.option,
      ...(config.value !== undefined && { value: config.value }),
    });
  }
  if (config.anyOf !== undefined) {
    return config.anyOf.some(checkCondition);
  }
  if (config.allOf !== undefined) {
    return config.allOf.some(checkCondition);
  }
  return false;
}

/**
 * Returns the primary CLI flag for a dependency option reference.
 * @since 0.11.0
 */
export function getDependeeFlagName(
  optionRef: string,
  context: ObjectFieldContext,
  objectKeys: readonly (string | symbol)[],
): string {
  const key = resolveDependeeKey(optionRef, objectKeys, context.flagToKey);
  if (key !== undefined) {
    return context.keyToPrimaryFlag.get(key) ?? optionRef;
  }
  return optionRef;
}

/**
 * Creates an error message for an unsatisfied required dependency.
 * @since 0.11.0
 */
export function formatRequiresOptionError(
  dependentOptionNames: readonly string[],
  dependeeFlag: string,
  expectedValue?: unknown,
): Message {
  if (expectedValue !== undefined) {
    return message`Option ${dependentOptionNames.join(", ")} requires option ${dependeeFlag} to be ${String(expectedValue)}.`;
  }
  return message`Option ${dependentOptionNames.join(", ")} requires option ${dependeeFlag}.`;
}

/**
 * Validates dependency constraints when a dependent option is parsed or completed.
 * @returns An error message when validation fails, or `undefined` when valid.
 * @since 0.11.0
 */
export function validateOptionDependency(
  config: DependsOnConfig,
  context: ObjectFieldContext,
  dependentOptionNames: readonly string[],
  dependentExplicitlyProvided: boolean,
): Message | undefined {
  if (!dependentExplicitlyProvided) return undefined;

  const satisfied = isDependsOnSatisfied(config, context);

  if (hasExplicitFalsyDependee(config, context) && !satisfied) {
    const objectKeys = Object.keys(context.parsers) as (string | symbol)[];
    const primaryRef = config.option ??
      findFirstOptionRef(config) ??
      "option";
    const flag = getDependeeFlagName(primaryRef, context, objectKeys);
    if (config.value !== undefined) {
      return formatRequiresOptionError(
        dependentOptionNames,
        flag,
        config.value,
      );
    }
    return formatRequiresOptionError(dependentOptionNames, flag);
  }

  if (!satisfied && config.required) {
    const objectKeys = Object.keys(context.parsers) as (string | symbol)[];
    const primaryRef = config.option ??
      findFirstOptionRef(config) ??
      "option";
    const flag = getDependeeFlagName(primaryRef, context, objectKeys);
    if (config.value !== undefined) {
      return formatRequiresOptionError(
        dependentOptionNames,
        flag,
        config.value,
      );
    }
    return formatRequiresOptionError(dependentOptionNames, flag);
  }

  return undefined;
}

function findFirstOptionRef(config: DependsOnConfig): string | undefined {
  if (config.option) return config.option;
  const findInCondition = (c: DependsOnCondition): string | undefined => {
    if (typeof c === "string") return c;
    if ("option" in c && typeof c.option === "string") return c.option;
    if ("anyOf" in c && c.anyOf?.length) {
      for (const sub of c.anyOf) {
        const found = findInCondition(sub);
        if (found) return found;
      }
    }
    if ("allOf" in c && c.allOf?.length) {
      for (const sub of c.allOf) {
        const found = findInCondition(sub);
        if (found) return found;
      }
    }
    return undefined;
  };
  if (config.anyOf?.length) {
    for (const c of config.anyOf) {
      const found = findInCondition(c);
      if (found) return found;
    }
  }
  if (config.allOf?.length) {
    for (const c of config.allOf) {
      const found = findInCondition(c);
      if (found) return found;
    }
  }
  return undefined;
}

/**
 * Returns whether a dependent option should be hidden from help and completion.
 * @since 0.11.0
 */
export function shouldHideDependentOption(
  config: DependsOnConfig,
  context: ObjectFieldContext | undefined,
): boolean {
  if (config.required) return false;
  if (!context) return false;
  return !isDependsOnSatisfied(config, context);
}

const objectFieldContextStack: ObjectFieldContext[] = [];

/**
 * Pushes an {@link ObjectFieldContext} for dependency evaluation during
 * {@link Parser.complete}.
 * @internal
 */
export function pushObjectFieldContext(context: ObjectFieldContext): void {
  objectFieldContextStack.push(context);
}

/**
 * Pops the most recently pushed {@link ObjectFieldContext}.
 * @internal
 */
export function popObjectFieldContext(): void {
  objectFieldContextStack.pop();
}

/**
 * Returns the current {@link ObjectFieldContext} during {@link Parser.complete}.
 * @internal
 */
export function peekObjectFieldContext(): ObjectFieldContext | undefined {
  return objectFieldContextStack.at(-1);
}

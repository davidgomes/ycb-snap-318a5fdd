import {
  isDeferredParseState,
  isDependencySourceState,
  isPendingDependencySourceState,
} from "./dependency.ts";
import {
  type Message,
  message,
  type MessageTerm,
  optionName,
} from "./message.ts";
import { type Usage, usageFieldKey, type UsageTerm } from "./usage.ts";

/**
 * A single option that another option depends on.
 *
 * `option` is either the object key produced by `object({ ... })` or a
 * CLI flag string such as `"--verbose"`. Flag strings are mapped to the
 * object key by walking usage terms, including terms nested under wrappers.
 *
 * @since 0.10.0
 */
export interface OptionDependency {
  /**
   * Object key or CLI flag that this dependency refers to.
   */
  readonly option: string;

  /**
   * When present, the dependency is satisfied only when the referenced
   * option equals this value. When omitted, the dependency is satisfied
   * only when the referenced option is truthy.
   */
  readonly value?: unknown;
}

/**
 * Dependency configuration stored on an option.
 *
 * A single dependency sets {@link option}. A compound dependency sets
 * {@link anyOf}, {@link allOf}, or both. `required` controls whether an
 * unsatisfied dependency is a validation error.
 *
 * @since 0.10.0
 */
export interface OptionDependsOn {
  /**
   * Object key or CLI flag that must be present.
   */
  readonly option?: string;

  /**
   * Expected value of {@link option}. Omit to require a truthy value.
   */
  readonly value?: unknown;

  /**
   * Satisfied when every listed dependency is satisfied.
   * An empty array is satisfied.
   */
  readonly allOf?: readonly OptionDependency[];

  /**
   * Satisfied when at least one listed dependency is satisfied.
   * An empty array is unsatisfied.
   */
  readonly anyOf?: readonly OptionDependency[];

  /**
   * When `true`, an unsatisfied dependency fails parsing.
   * The error includes the dependee's CLI flag and, when a value
   * constraint is set, the expected value.
   *
   * When omitted or `false`, an unsatisfied dependency hides the option
   * from help and completion. Parsing still accepts the option unless the
   * dependee was explicitly provided with a falsy value.
   */
  readonly required?: boolean;
}

/**
 * Condition accepted by {@link requiredWhen}, {@link optionalWhen}, and
 * {@link conditionalOption}.
 *
 * A string names an object key or CLI flag and is satisfied when that
 * option is truthy. An object may be a single dependency, an
 * `anyOf`/`allOf` group, or a full {@link OptionDependsOn} value.
 *
 * @since 0.10.0
 */
export type DependencyCondition =
  | string
  | OptionDependency
  | OptionDependsOn
  | {
    readonly option?: string;
    readonly value?: unknown;
    readonly anyOf?: readonly (string | OptionDependency)[];
    readonly allOf?: readonly (string | OptionDependency)[];
    readonly required?: boolean;
  };

/**
 * Parser fields needed to resolve option dependencies.
 * @internal
 */
export interface DependencyParserRef {
  readonly usage: Usage;
  readonly initialState: unknown;
}

/**
 * Maps object keys and CLI flags for one `object()` parser.
 * @internal
 */
export interface OptionIndex {
  readonly initialStates: ReadonlyMap<PropertyKey, unknown>;
  readonly keyToFlags: ReadonlyMap<PropertyKey, readonly string[]>;
  readonly flagToKey: ReadonlyMap<string, PropertyKey>;
}

interface InterpretedField {
  readonly explicit: boolean;
  readonly value: unknown;
  readonly found: boolean;
}

interface ConditionResult {
  readonly satisfied: boolean;
  readonly explicitFalsy: boolean;
  readonly failures: readonly DependencyFailure[];
}

interface DependencyFailure {
  readonly flag: string;
  readonly hasExpectedValue: boolean;
  readonly expectedValue?: unknown;
}

/**
 * Result of evaluating a {@link OptionDependsOn} configuration.
 * @internal
 */
export interface DependencyEvaluation {
  readonly satisfied: boolean;
  readonly explicitFalsy: boolean;
  readonly failures: readonly DependencyFailure[];
}

/**
 * Normalizes a helper condition into an {@link OptionDependsOn} value.
 * @internal
 */
export function normalizeDependsOn(
  condition: DependencyCondition,
): OptionDependsOn {
  if (typeof condition === "string") {
    return { option: condition };
  }

  const normalized: {
    option?: string;
    value?: unknown;
    anyOf?: OptionDependency[];
    allOf?: OptionDependency[];
    required?: boolean;
  } = {};

  if (typeof condition.option === "string") {
    normalized.option = condition.option;
    if (Object.prototype.hasOwnProperty.call(condition, "value")) {
      normalized.value = condition.value;
    }
  }
  if ("anyOf" in condition && condition.anyOf != null) {
    normalized.anyOf = condition.anyOf.map(normalizeDependencyRef);
  }
  if ("allOf" in condition && condition.allOf != null) {
    normalized.allOf = condition.allOf.map(normalizeDependencyRef);
  }
  if (
    "required" in condition &&
    typeof condition.required === "boolean"
  ) {
    normalized.required = condition.required;
  }
  return normalized;
}

/**
 * Returns a copy of `dependsOn` with `required` forced to `required`.
 * @internal
 */
export function withRequiredFlag(
  dependsOn: OptionDependsOn,
  required: boolean,
): OptionDependsOn {
  return { ...dependsOn, required };
}

function normalizeDependencyRef(
  ref: string | OptionDependency,
): OptionDependency {
  if (typeof ref === "string") return { option: ref };
  if (Object.prototype.hasOwnProperty.call(ref, "value")) {
    return { option: ref.option, value: ref.value };
  }
  return { option: ref.option };
}

/**
 * Builds an index from object fields to CLI flags and initial states.
 * @internal
 */
export function buildOptionIndex(
  pairs: readonly (readonly [PropertyKey, DependencyParserRef])[],
): OptionIndex {
  const initialStates = new Map<PropertyKey, unknown>();
  const keyToFlags = new Map<PropertyKey, readonly string[]>();
  const flagToKey = new Map<string, PropertyKey>();

  for (const [key, parser] of pairs) {
    if (parser == null) continue;
    const names = collectOptionNames(parser.usage);
    initialStates.set(key, parser.initialState);
    keyToFlags.set(key, names);
    for (const name of names) {
      if (!flagToKey.has(name)) flagToKey.set(name, key);
    }
  }

  return { initialStates, keyToFlags, flagToKey };
}

/**
 * Rebuilds an option index from usage terms stamped with {@link usageFieldKey}.
 * @internal
 */
export function buildOptionIndexFromUsage(usage: Usage): OptionIndex {
  const initialStates = new Map<PropertyKey, unknown>();
  const keyToFlags = new Map<PropertyKey, string[]>();
  const flagToKey = new Map<string, PropertyKey>();

  const visit = (terms: Usage): void => {
    for (const term of terms) {
      if (term.type === "option") {
        const key = term[usageFieldKey];
        if (key == null) continue;
        const existing = keyToFlags.get(key) ?? [];
        const names = [...existing, ...term.names];
        keyToFlags.set(key, names);
        if (!initialStates.has(key)) initialStates.set(key, undefined);
        for (const name of term.names) {
          if (!flagToKey.has(name)) flagToKey.set(name, key);
        }
      } else if (term.type === "optional" || term.type === "multiple") {
        visit(term.terms);
      } else if (term.type === "exclusive") {
        for (const branch of term.terms) visit(branch);
      }
    }
  };
  visit(usage);

  return { initialStates, keyToFlags, flagToKey };
}

/**
 * Stamps each option term with the object field that owns it.
 * @internal
 */
export function annotateUsageFieldKeys(
  pairs: readonly (readonly [PropertyKey, DependencyParserRef])[],
): Usage {
  const terms: UsageTerm[] = [];
  for (const [key, parser] of pairs) {
    if (parser == null) continue;
    terms.push(...stampUsage(parser.usage, key));
  }
  return terms;
}

function fieldKey(key: PropertyKey): string | symbol {
  return typeof key === "number" ? String(key) : key;
}

function stampUsage(usage: Usage, key: PropertyKey): Usage {
  return usage.map((term) => stampTerm(term, key));
}

function stampTerm(term: UsageTerm, key: PropertyKey): UsageTerm {
  if (term.type === "option") {
    return { ...term, [usageFieldKey]: fieldKey(key) };
  }
  if (term.type === "optional") {
    return { ...term, terms: stampUsage(term.terms, key) };
  }
  if (term.type === "multiple") {
    return { ...term, terms: stampUsage(term.terms, key) };
  }
  if (term.type === "exclusive") {
    return {
      ...term,
      terms: term.terms.map((branch) => stampUsage(branch, key)),
    };
  }
  return term;
}

/**
 * Returns the first `dependsOn` configuration found in a usage tree.
 * @internal
 */
export function findDependsOn(usage: Usage): OptionDependsOn | undefined {
  for (const term of usage) {
    if (term.type === "option") {
      if (term.dependsOn != null) return term.dependsOn;
    } else if (term.type === "optional" || term.type === "multiple") {
      const nested = findDependsOn(term.terms);
      if (nested != null) return nested;
    } else if (term.type === "exclusive") {
      for (const branch of term.terms) {
        const nested = findDependsOn(branch);
        if (nested != null) return nested;
      }
    }
  }
  return undefined;
}

/**
 * Whether `usage` contains any conditional option dependency.
 * @internal
 */
export function usageHasOptionDependencies(usage: Usage): boolean {
  return findDependsOn(usage) != null;
}

/**
 * An unsatisfied, non-required dependency hides the option from help
 * and completion. Metadata is read from the usage term.
 * @internal
 */
export function isDependencyHidden(
  usage: Usage,
  state: unknown,
  index: OptionIndex,
): boolean {
  const dependsOn = findDependsOn(usage);
  if (dependsOn == null || dependsOn.required === true) return false;
  return !evaluateDependsOn(dependsOn, state, index).satisfied;
}

/**
 * Validates object fields against their `dependsOn` metadata.
 *
 * This function never calls `complete`. Missing parsers and undefined
 * states are treated as unsatisfied dependencies.
 *
 * @internal
 */
export function optionDependencyError(
  pairs: readonly (readonly [PropertyKey, DependencyParserRef])[],
  state: unknown,
): Message | undefined {
  const index = buildOptionIndex(pairs);
  const record = locateStateRecord(state, index);

  for (const [key, parser] of pairs) {
    if (parser == null) continue;
    const dependsOn = findDependsOn(parser.usage);
    if (dependsOn == null) continue;

    const evaluation = evaluateDependsOn(dependsOn, record, index);
    if (evaluation.satisfied) continue;

    const fieldState = readOwn(record, key);
    const dependent = interpretField(
      fieldState,
      index.initialStates.get(key),
    );
    const failed = dependsOn.required === true ||
      (dependent.explicit && evaluation.explicitFalsy);
    if (!failed) continue;

    const flag = primaryFlag(parser.usage) ??
      (typeof key === "string" ? key : "option");
    return dependencyErrorMessage(flag, evaluation.failures);
  }
  return undefined;
}

/**
 * Evaluates a dependency against parser state.
 *
 * Wrapped states (`{ success, value }`, single-element arrays, dependency
 * source states) and plain values are both accepted. `complete` is not
 * called, so an undefined parser or state cannot be completed.
 *
 * @internal
 */
export function evaluateDependsOn(
  dependsOn: OptionDependsOn,
  state: unknown,
  index: OptionIndex,
): DependencyEvaluation {
  const parts: ConditionResult[] = [];

  if (typeof dependsOn.option === "string") {
    parts.push(
      evaluateRef(
        hasOwn(dependsOn, "value")
          ? { option: dependsOn.option, value: dependsOn.value }
          : { option: dependsOn.option },
        state,
        index,
      ),
    );
  }

  if (dependsOn.allOf != null) {
    for (const ref of dependsOn.allOf) {
      parts.push(evaluateRef(ref, state, index));
    }
  }

  let anyOf: ConditionResult | undefined;
  if (dependsOn.anyOf != null) {
    if (dependsOn.anyOf.length === 0) {
      anyOf = { satisfied: false, explicitFalsy: false, failures: [] };
    } else {
      const results = dependsOn.anyOf.map((ref) =>
        evaluateRef(ref, state, index)
      );
      const satisfied = results.some((result) => result.satisfied);
      anyOf = {
        satisfied,
        explicitFalsy: !satisfied &&
          results.some((result) => result.explicitFalsy),
        failures: satisfied ? [] : results.flatMap((result) => result.failures),
      };
    }
  }

  const allSatisfied = parts.every((part) => part.satisfied) &&
    (anyOf?.satisfied ?? true);
  const failures = allSatisfied ? [] : [
    ...parts.filter((part) => !part.satisfied).flatMap((part) => part.failures),
    ...(anyOf != null && !anyOf.satisfied ? anyOf.failures : []),
  ];
  const explicitFalsy = !allSatisfied &&
    (parts.some((part) => !part.satisfied && part.explicitFalsy) ||
      (anyOf?.explicitFalsy ?? false));

  return { satisfied: allSatisfied, explicitFalsy, failures };
}

function evaluateRef(
  ref: OptionDependency,
  state: unknown,
  index: OptionIndex,
): ConditionResult {
  const key = resolveRef(ref.option, index);
  const hasExpectedValue = hasOwn(ref, "value");
  const failure: DependencyFailure = {
    flag: displayFlag(ref.option, key, index),
    hasExpectedValue,
    ...(hasExpectedValue ? { expectedValue: ref.value } : {}),
  };

  if (key == null) {
    return {
      satisfied: false,
      explicitFalsy: false,
      failures: [failure],
    };
  }

  const field = interpretField(
    readOwn(locateStateRecord(state, index), key),
    index.initialStates.get(key),
  );
  const satisfied = hasExpectedValue
    ? Object.is(field.value, ref.value)
    : isTruthy(field.value);
  const explicitFalsy = field.explicit && !satisfied && !isTruthy(field.value);
  return {
    satisfied,
    explicitFalsy,
    failures: satisfied ? [] : [failure],
  };
}

function resolveRef(
  ref: string,
  index: OptionIndex,
): PropertyKey | undefined {
  if (index.initialStates.has(ref)) return ref;
  return index.flagToKey.get(ref);
}

function displayFlag(
  ref: string,
  key: PropertyKey | undefined,
  index: OptionIndex,
): string {
  if (key != null) {
    const flags = index.keyToFlags.get(key);
    if (flags != null && flags.length > 0) return preferredFlag(flags);
  }
  return ref;
}

/**
 * Prefers the longest GNU-style long option as the user-facing flag.
 * @internal
 */
export function preferredFlag(names: readonly string[]): string {
  const longOptions = names.filter((name) => name.startsWith("--"));
  const pool = longOptions.length > 0 ? longOptions : names;
  return pool.reduce((best, name) => name.length > best.length ? name : best);
}

function primaryFlag(usage: Usage): string | undefined {
  const names = collectOptionNames(usage);
  if (names.length === 0) return undefined;
  return preferredFlag(names);
}

function collectOptionNames(usage: Usage): readonly string[] {
  const names: string[] = [];
  const visit = (terms: Usage): void => {
    for (const term of terms) {
      if (term.type === "option") {
        names.push(...term.names);
      } else if (term.type === "optional" || term.type === "multiple") {
        visit(term.terms);
      } else if (term.type === "exclusive") {
        for (const branch of term.terms) visit(branch);
      }
    }
  };
  visit(usage);
  return names;
}

function dependencyErrorMessage(
  dependentFlag: string,
  failures: readonly DependencyFailure[],
): Message {
  const parts: MessageTerm[] = [
    ...message`Option ${optionName(dependentFlag)} requires option`,
  ];
  if (failures.length === 0) {
    parts.push({ type: "text", text: "." });
    return parts;
  }
  for (let i = 0; i < failures.length; i++) {
    const failure = failures[i];
    parts.push({ type: "text", text: i === 0 ? " " : " and " });
    parts.push(optionName(failure.flag));
    if (failure.hasExpectedValue) {
      parts.push({
        type: "text",
        text: ` ${formatExpectedValue(failure.expectedValue)}`,
      });
    }
  }
  parts.push({ type: "text", text: "." });
  return parts;
}

function formatExpectedValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (
    typeof value === "number" || typeof value === "boolean" ||
    typeof value === "bigint"
  ) {
    return String(value);
  }
  if (value === null) return "null";
  if (value === undefined) return "undefined";
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function locateStateRecord(
  state: unknown,
  index: OptionIndex,
): Record<PropertyKey, unknown> | undefined {
  const found = searchStateRecord(state, index);
  if (found != null) return found;
  if (isPlainRecord(state) && !Array.isArray(state) && !isSpecialState(state)) {
    return state;
  }
  return undefined;
}

function searchStateRecord(
  state: unknown,
  index: OptionIndex,
): Record<PropertyKey, unknown> | undefined {
  if (Array.isArray(state)) {
    for (const item of state) {
      const found = searchStateRecord(item, index);
      if (found != null) return found;
    }
    return undefined;
  }
  if (!isPlainRecord(state) || isSpecialState(state)) return undefined;
  if (recordHasIndexKey(state, index)) return state;
  if (isValueResult(state) && state.success && isPlainRecord(state.value)) {
    const inner = state.value;
    if (!isSpecialState(inner) && recordHasIndexKey(inner, index)) {
      return inner;
    }
  }
  return undefined;
}

function recordHasIndexKey(
  record: Record<PropertyKey, unknown>,
  index: OptionIndex,
): boolean {
  for (const key of index.initialStates.keys()) {
    if (key in record) return true;
  }
  for (const key of index.flagToKey.keys()) {
    if (key in record) return true;
  }
  return false;
}

function readOwn(
  record: Record<PropertyKey, unknown> | undefined,
  key: PropertyKey,
): unknown {
  if (record == null || !(key in record)) return undefined;
  return record[key];
}

/**
 * Interprets a field state without calling `complete`.
 * Undefined states stay undefined.
 * @internal
 */
export function interpretField(
  field: unknown,
  initialState: unknown,
): InterpretedField {
  if (field === initialState) {
    return {
      explicit: false,
      value: peekValue(field),
      found: field !== undefined,
    };
  }
  if (field === undefined) {
    return { explicit: false, value: undefined, found: false };
  }
  return { ...unwrapField(field), found: true };
}

function unwrapField(
  field: unknown,
): { explicit: boolean; value: unknown } {
  if (field == null) return { explicit: false, value: undefined };
  if (Array.isArray(field)) {
    if (field.length === 0) return { explicit: false, value: undefined };
    if (field.length === 1) return unwrapField(field[0]);
    const items = field.map((item) => unwrapField(item));
    return {
      explicit: items.some((item) => item.explicit),
      value: items.map((item) => item.value),
    };
  }
  if (isPendingDependencySourceState(field)) {
    return { explicit: false, value: undefined };
  }
  if (isDependencySourceState(field)) {
    const result = field.result;
    if (result.success) return { explicit: true, value: result.value };
    return { explicit: false, value: undefined };
  }
  if (isDeferredParseState(field)) {
    const result = field.preliminaryResult;
    if (result.success) return { explicit: true, value: result.value };
    return { explicit: false, value: undefined };
  }
  if (isValueResult(field)) {
    if (field.success) return { explicit: true, value: field.value };
    return { explicit: false, value: undefined };
  }
  return { explicit: true, value: field };
}

function peekValue(field: unknown): unknown {
  return unwrapField(field).value;
}

function isValueResult(
  value: object,
): value is { success: boolean; value?: unknown } {
  if (!("success" in value)) return false;
  const success = (value as { success?: unknown }).success;
  return success === true || success === false;
}

function isSpecialState(value: object): boolean {
  return isPendingDependencySourceState(value) ||
    isDependencySourceState(value) ||
    isDeferredParseState(value);
}

function isPlainRecord(
  value: unknown,
): value is Record<PropertyKey, unknown> {
  return typeof value === "object" && value !== null;
}

function isTruthy(value: unknown): boolean {
  return Boolean(value);
}

function hasOwn(value: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

/**
 * Drops option terms whose non-required dependency is unsatisfied.
 * @internal
 */
export function filterUsageByDependencies(
  usage: Usage,
  state: unknown,
): Usage {
  const index = buildOptionIndexFromUsage(usage);
  const record = locateStateRecord(state, index);
  return filterTerms(usage, record, index);
}

function filterTerms(
  terms: Usage,
  state: unknown,
  index: OptionIndex,
): UsageTerm[] {
  const result: UsageTerm[] = [];
  for (const term of terms) {
    if (term.type === "option") {
      if (term.hidden) {
        result.push(term);
        continue;
      }
      if (
        term.dependsOn != null && term.dependsOn.required !== true &&
        !evaluateDependsOn(term.dependsOn, state, index).satisfied
      ) {
        continue;
      }
      result.push(term);
    } else if (term.type === "optional" || term.type === "multiple") {
      const inner = filterTerms(term.terms, state, index);
      if (inner.length === 0) continue;
      result.push({ ...term, terms: inner });
    } else if (term.type === "exclusive") {
      const branches = term.terms
        .map((branch) => filterTerms(branch, state, index))
        .filter((branch) => branch.length > 0);
      if (branches.length === 0) continue;
      result.push({ ...term, terms: branches });
    } else {
      result.push(term);
    }
  }
  return result;
}

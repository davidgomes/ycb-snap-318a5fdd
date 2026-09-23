import {
  isDeferredParseState,
  isDependencySourceState,
  isPendingDependencySourceState,
} from "./dependency.ts";
import {
  type Message,
  message,
  optionName as eOptionName,
  text,
} from "./message.ts";
import type {
  DependencyCondition,
  DependsOn,
  DependsOnNode,
  Usage,
  UsageTerm,
} from "./usage.ts";

export type { DependencyCondition, DependsOn, DependsOnNode };

/**
 * Symbol for a parser method that returns usage with unsatisfied
 * conditional options removed. Help generation calls it with the
 * current parser state.
 *
 * @since 0.10.0
 */
export const visibleUsage: unique symbol = Symbol.for(
  "@optique/core/option-dependency/visibleUsage",
);

/**
 * A field participating in conditional option checks.
 * @internal
 */
export interface DependencyField {
  readonly key: PropertyKey;
  readonly usage: Usage;
  readonly initialState: unknown;
  readonly optionNames: readonly string[];
}

interface Failure {
  readonly flag: string;
  readonly hasValue: boolean;
  readonly expected: unknown;
}

interface Evaluation {
  readonly satisfied: boolean;
  readonly explicitFalsy: boolean;
  readonly failures: readonly Failure[];
}

interface InterpretedField {
  readonly explicit: boolean;
  readonly failed: boolean;
  readonly value: unknown;
}

const satisfiedEval: Evaluation = {
  satisfied: true,
  explicitFalsy: false,
  failures: [],
};

/**
 * Builds a {@link DependsOn} value from a helper condition.
 *
 * @param condition String name, single condition, or compound condition.
 * @param presence Whether the helper forces `required`.
 * @returns The dependency configuration stored on the option.
 * @since 0.10.0
 */
export function toDependsOn(
  condition: DependencyCondition,
  presence: "required" | "optional" | "preserve",
): DependsOn {
  const base: DependsOn = typeof condition === "string"
    ? { option: condition }
    : { ...condition };
  if (presence === "preserve") return base;
  if (presence === "required") return { ...base, required: true };
  if (base.required === undefined) return base;
  return {
    ...(base.option !== undefined ? { option: base.option } : {}),
    ...("value" in base ? { value: base.value } : {}),
    ...(base.anyOf !== undefined ? { anyOf: base.anyOf } : {}),
    ...(base.allOf !== undefined ? { allOf: base.allOf } : {}),
  };
}

/**
 * Reads conditional dependency metadata from a usage tree.
 * Wrappers such as `withDefault()` nest the original option term, so
 * callers must use this instead of looking at the parser object.
 *
 * @param usage Usage terms to search.
 * @returns The first `dependsOn` configuration, if any.
 * @since 0.10.0
 */
export function findDependsOn(usage: Usage | undefined): DependsOn | undefined {
  if (!Array.isArray(usage)) return undefined;
  for (const term of usage) {
    if (term.type === "option" && term.dependsOn != null) {
      return term.dependsOn;
    }
    if (term.type === "optional" || term.type === "multiple") {
      const found = findDependsOn(term.terms);
      if (found) return found;
    } else if (term.type === "exclusive") {
      for (const group of term.terms) {
        const found = findDependsOn(group);
        if (found) return found;
      }
    }
  }
  return undefined;
}

/**
 * Collects option names from usage, including hidden options.
 * Flag strings in `dependsOn.option` are resolved through this list.
 */
function collectOptionNames(usage: Usage | undefined): string[] {
  const names: string[] = [];
  walk(usage);
  return names;

  function walk(terms: Usage | undefined): void {
    if (!Array.isArray(terms)) return;
    for (const term of terms) {
      if (term.type === "option") {
        names.push(...term.names);
      } else if (term.type === "optional" || term.type === "multiple") {
        walk(term.terms);
      } else if (term.type === "exclusive") {
        for (const group of term.terms) walk(group);
      }
    }
  }
}

/**
 * Builds the field catalog used to resolve dependency references.
 * Undefined parsers are skipped so dependency checks never call methods
 * on them.
 *
 * @param pairs Object fields in parser order.
 * @returns Catalog entries for parsers that expose usage terms.
 * @since 0.10.0
 */
export function catalogFromParsers(
  pairs: readonly (readonly [
    PropertyKey,
    | { readonly usage?: Usage; readonly initialState?: unknown }
    | null
    | undefined,
  ])[],
): DependencyField[] {
  const fields: DependencyField[] = [];
  for (const [key, parser] of pairs) {
    if (parser == null || typeof parser !== "object") continue;
    if (!Array.isArray(parser.usage)) continue;
    fields.push({
      key,
      usage: parser.usage,
      initialState: parser.initialState,
      optionNames: collectOptionNames(parser.usage),
    });
  }
  return fields;
}

function asRecord(
  state: unknown,
): Record<string | symbol, unknown> | undefined {
  if (state == null || typeof state !== "object" || Array.isArray(state)) {
    return undefined;
  }
  return state as Record<string | symbol, unknown>;
}

function deepEqual(left: unknown, right: unknown, depth = 0): boolean {
  if (Object.is(left, right)) return true;
  if (depth > 20) return false;
  if (typeof left !== "object" || typeof right !== "object") return false;
  if (left === null || right === null) return false;
  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right)) return false;
    if (left.length !== right.length) return false;
    for (let index = 0; index < left.length; index++) {
      if (!deepEqual(left[index], right[index], depth + 1)) return false;
    }
    return true;
  }
  const leftKeys = Reflect.ownKeys(left);
  const rightKeys = Reflect.ownKeys(right);
  if (leftKeys.length !== rightKeys.length) return false;
  const rightKeySet = new Set<PropertyKey>(rightKeys);
  for (const key of leftKeys) {
    if (!rightKeySet.has(key)) return false;
    const leftRecord = left as Record<PropertyKey, unknown>;
    const rightRecord = right as Record<PropertyKey, unknown>;
    if (!deepEqual(leftRecord[key], rightRecord[key], depth + 1)) return false;
  }
  return true;
}

type Extracted =
  | { readonly kind: "value"; readonly value: unknown }
  | { readonly kind: "missing" }
  | { readonly kind: "failed" };

/**
 * Unwraps parser results and wrapper states such as `withDefault()`'s
 * single-element arrays. Does not call `complete()`.
 */
function extractValue(state: unknown, depth = 0): Extracted {
  if (depth > 10) return { kind: "value", value: state };
  if (state == null) return { kind: "missing" };
  if (Array.isArray(state)) {
    if (state.length === 1) return extractValue(state[0], depth + 1);
    if (state.length === 0) return { kind: "missing" };
    return { kind: "value", value: state };
  }
  if (isDependencySourceState(state)) {
    return extractValue(state.result, depth + 1);
  }
  if (isDeferredParseState(state)) {
    return extractValue(state.preliminaryResult, depth + 1);
  }
  if (isPendingDependencySourceState(state)) return { kind: "missing" };
  if (typeof state === "object" && "success" in state) {
    const result = state as { success?: unknown; value?: unknown };
    if (result.success === true) return { kind: "value", value: result.value };
    return { kind: "failed" };
  }
  return { kind: "value", value: state };
}

function interpretField(
  state: unknown,
  initialState: unknown,
): InterpretedField {
  const untouched = state === initialState || deepEqual(state, initialState);
  const source = untouched
    ? (state === undefined ? initialState : state)
    : state;
  const extracted = extractValue(source);
  return {
    explicit: !untouched && extracted.kind === "value",
    failed: !untouched && extracted.kind === "failed",
    value: extracted.kind === "value" ? extracted.value : undefined,
  };
}

function isTruthy(value: unknown): boolean {
  return Boolean(value);
}

function sameValue(actual: unknown, expected: unknown): boolean {
  if (Object.is(actual, expected)) return true;
  if (
    typeof actual === "object" && actual !== null &&
    typeof expected === "object" && expected !== null
  ) {
    try {
      return JSON.stringify(actual) === JSON.stringify(expected);
    } catch {
      return false;
    }
  }
  return false;
}

function primaryFlag(names: readonly string[], fallback: string): string {
  const long = names.find((name) => name.startsWith("--"));
  if (long != null) return long;
  return names[0] ?? fallback;
}

function resolveKey(
  reference: string,
  catalog: readonly DependencyField[],
): PropertyKey | undefined {
  for (const field of catalog) {
    if (field.key === reference) return field.key;
  }
  for (const field of catalog) {
    if (field.optionNames.includes(reference)) return field.key;
  }
  return undefined;
}

function evaluateSingle(
  node: DependsOn,
  catalog: readonly DependencyField[],
  state: unknown,
): Evaluation {
  const reference = node.option ?? "";
  const expectsValue = Object.prototype.hasOwnProperty.call(node, "value");
  const key = resolveKey(reference, catalog);
  if (key === undefined) {
    return {
      satisfied: false,
      explicitFalsy: false,
      failures: [{
        flag: reference,
        hasValue: expectsValue,
        expected: node.value,
      }],
    };
  }
  const field = catalog.find((entry) => entry.key === key);
  const record = asRecord(state);
  const interpreted = interpretField(
    record == null ? undefined : record[key],
    field?.initialState,
  );
  const satisfied = expectsValue
    ? sameValue(interpreted.value, node.value)
    : isTruthy(interpreted.value);
  const explicitFalsy = !expectsValue && interpreted.explicit &&
    !isTruthy(interpreted.value);
  if (satisfied) return satisfiedEval;
  return {
    satisfied: false,
    explicitFalsy,
    failures: [{
      flag: primaryFlag(field?.optionNames ?? [], reference),
      hasValue: expectsValue,
      expected: node.value,
    }],
  };
}

function combineAll(parts: readonly Evaluation[]): Evaluation {
  if (parts.length === 0) return satisfiedEval;
  const satisfied = parts.every((part) => part.satisfied);
  return {
    satisfied,
    explicitFalsy: parts.some((part) => part.explicitFalsy),
    failures: satisfied ? [] : parts.flatMap((part) => part.failures),
  };
}

function combineAny(parts: readonly Evaluation[]): Evaluation {
  if (parts.length === 0) {
    return { satisfied: false, explicitFalsy: false, failures: [] };
  }
  if (parts.some((part) => part.satisfied)) return satisfiedEval;
  return {
    satisfied: false,
    explicitFalsy: parts.every((part) => part.explicitFalsy),
    failures: parts.flatMap((part) => part.failures),
  };
}

function evaluateNode(
  node: DependsOnNode,
  catalog: readonly DependencyField[],
  state: unknown,
): Evaluation {
  if (typeof node === "string") {
    return evaluateSingle({ option: node }, catalog, state);
  }
  const parts: Evaluation[] = [];
  if (typeof node.option === "string") {
    parts.push(evaluateSingle(node, catalog, state));
  }
  if (node.allOf != null) {
    parts.push(
      combineAll(
        node.allOf.map((child) => evaluateNode(child, catalog, state)),
      ),
    );
  }
  if (node.anyOf != null) {
    parts.push(
      combineAny(
        node.anyOf.map((child) => evaluateNode(child, catalog, state)),
      ),
    );
  }
  if (parts.length === 0) {
    return { satisfied: false, explicitFalsy: false, failures: [] };
  }
  return combineAll(parts);
}

/**
 * Evaluates a dependency against the current object state.
 * Missing keys and unknown flags are unsatisfied. This function never
 * calls `complete()` or `suggest()`.
 *
 * @param dependsOn Dependency configuration from a usage term.
 * @param catalog Fields in the same object parser.
 * @param state Current object state, wrapped or plain.
 * @returns Whether the dependency holds and why it failed.
 * @since 0.10.0
 */
export function evaluateDependsOn(
  dependsOn: DependsOn,
  catalog: readonly DependencyField[],
  state: unknown,
): Evaluation {
  return evaluateNode(dependsOn, catalog, state);
}

/**
 * Whether an option should be omitted from help and completion because
 * its dependency is unsatisfied and not required.
 *
 * @param usage Usage of the field parser. Dependency metadata is read
 *              from these terms.
 * @param catalog Sibling fields.
 * @param state Current object state.
 * @returns `true` when the option is conditionally hidden.
 * @since 0.10.0
 */
export function isOptionHiddenByDependency(
  usage: Usage | undefined,
  catalog: readonly DependencyField[],
  state: unknown,
): boolean {
  const dependsOn = findDependsOn(usage);
  if (dependsOn == null || dependsOn.required === true) return false;
  return !evaluateDependsOn(dependsOn, catalog, state).satisfied;
}

/**
 * Unsatisfied dependencies should not block an empty parse. Required
 * ones fail later with a `requires option` error. Optional ones are
 * simply absent.
 *
 * @param usage Field usage.
 * @param catalog Sibling fields.
 * @param state Current object state.
 * @returns `true` when a missing value from this field can be ignored.
 * @since 0.10.0
 */
export function shouldIgnoreIncompleteField(
  usage: Usage | undefined,
  catalog: readonly DependencyField[],
  state: unknown,
): boolean {
  const dependsOn = findDependsOn(usage);
  if (dependsOn == null) return false;
  return !evaluateDependsOn(dependsOn, catalog, state).satisfied;
}

/**
 * Whether any required dependency in the catalog is currently unsatisfied.
 *
 * @param catalog Sibling fields.
 * @param state Current object state.
 * @returns `true` when parsing should surface a dependency error.
 * @since 0.10.0
 */
export function hasUnsatisfiedRequiredDependency(
  catalog: readonly DependencyField[],
  state: unknown,
): boolean {
  for (const field of catalog) {
    const dependsOn = findDependsOn(field.usage);
    if (dependsOn == null || dependsOn.required !== true) continue;
    const record = asRecord(state);
    const interpreted = interpretField(
      record == null ? undefined : record[field.key],
      field.initialState,
    );
    if (!interpreted.explicit) continue;
    if (!evaluateDependsOn(dependsOn, catalog, state).satisfied) return true;
  }
  return false;
}

function formatExpected(value: unknown): string {
  if (typeof value === "string") return value;
  if (
    typeof value === "number" || typeof value === "boolean" ||
    typeof value === "bigint" || value == null
  ) {
    return String(value);
  }
  try {
    return JSON.stringify(value) ?? String(value);
  } catch {
    return String(value);
  }
}

function dedupeFailures(failures: readonly Failure[]): Failure[] {
  const seen = new Set<string>();
  const unique: Failure[] = [];
  for (const failure of failures) {
    const id = `${failure.flag}\0${failure.hasValue ? "1" : "0"}\0${
      formatExpected(failure.expected)
    }`;
    if (seen.has(id)) continue;
    seen.add(id);
    unique.push(failure);
  }
  return unique;
}

function formatRequiresOption(
  field: DependencyField,
  evaluation: Evaluation,
): Message {
  const dependent = primaryFlag(field.optionNames, String(field.key));
  const failures = dedupeFailures(evaluation.failures);
  if (failures.length === 0) {
    return message`${eOptionName(dependent)} requires option.`;
  }
  if (failures.length === 1) {
    const failure = failures[0];
    if (failure.hasValue) {
      return message`${eOptionName(dependent)} requires option ${
        eOptionName(failure.flag)
      } ${text(formatExpected(failure.expected))}.`;
    }
    return message`${eOptionName(dependent)} requires option ${
      eOptionName(failure.flag)
    }.`;
  }
  const detail = failures.map((failure) =>
    failure.hasValue
      ? `${failure.flag} ${formatExpected(failure.expected)}`
      : failure.flag
  ).join(", ");
  return message`${eOptionName(dependent)} requires option ${text(detail)}.`;
}

/**
 * Applies conditional option rules after each field has been completed.
 *
 * Required unsatisfied dependencies fail with an error that includes
 * `requires option` and the dependee flag. An explicitly provided falsy
 * dependee also rejects the dependent option. Unsatisfied optional
 * dependencies suppress a missing-option error when the option was not
 * supplied, and still accept an explicitly supplied value.
 *
 * @param pairs Object fields.
 * @param rawState Parser state before completion. Wrapped and plain
 *                 field values are both accepted.
 * @param values Completed field values. Updated when a missing optional
 *               dependent option is suppressed.
 * @param errors Field completion errors keyed by object key.
 * @returns The object value, or a validation error.
 * @since 0.10.0
 */
export function enforceOptionDependencies(
  pairs: readonly (readonly [
    PropertyKey,
    | { readonly usage?: Usage; readonly initialState?: unknown }
    | null
    | undefined,
  ])[],
  rawState: unknown,
  values: Record<PropertyKey, unknown>,
  errors: ReadonlyMap<PropertyKey, Message>,
): { success: true; value: Record<PropertyKey, unknown> } | {
  success: false;
  error: Message;
} {
  const catalog = catalogFromParsers(pairs);
  const record = asRecord(rawState);

  for (const field of catalog) {
    const dependsOn = findDependsOn(field.usage);
    if (dependsOn == null) continue;
    const evaluation = evaluateDependsOn(dependsOn, catalog, rawState);
    const interpreted = interpretField(
      record == null ? undefined : record[field.key],
      field.initialState,
    );
    if (
      dependsOn.required === true && interpreted.explicit &&
      !evaluation.satisfied
    ) {
      return {
        success: false,
        error: formatRequiresOption(field, evaluation),
      };
    }
    if (
      interpreted.explicit && !evaluation.satisfied && evaluation.explicitFalsy
    ) {
      return {
        success: false,
        error: formatRequiresOption(field, evaluation),
      };
    }
  }

  for (const [key] of pairs) {
    const error = errors.get(key);
    if (error == null) continue;
    const field = catalog.find((entry) => entry.key === key);
    const dependsOn = field == null ? undefined : findDependsOn(field.usage);
    if (dependsOn != null && field != null) {
      const evaluation = evaluateDependsOn(dependsOn, catalog, rawState);
      const interpreted = interpretField(
        record == null ? undefined : record[field.key],
        field.initialState,
      );
      if (
        !evaluation.satisfied && !interpreted.explicit && !interpreted.failed
      ) {
        values[key] = undefined;
        continue;
      }
    }
    return { success: false, error };
  }

  return { success: true, value: values };
}

function hiddenFlags(
  catalog: readonly DependencyField[],
  state: unknown,
): Set<string> {
  const hidden = new Set<string>();
  for (const field of catalog) {
    if (!isOptionHiddenByDependency(field.usage, catalog, state)) continue;
    for (const name of field.optionNames) hidden.add(name);
  }
  return hidden;
}

function filterTerm(
  term: UsageTerm,
  hidden: ReadonlySet<string>,
): UsageTerm | undefined {
  if (term.type === "option") {
    if (term.names.some((name) => hidden.has(name))) return undefined;
    return term;
  }
  if (term.type === "optional" || term.type === "multiple") {
    const terms = filterUsage(term.terms, hidden);
    if (terms.length === 0) return undefined;
    if (terms === term.terms) return term;
    return term.type === "optional"
      ? { type: "optional", terms }
      : { type: "multiple", terms, min: term.min };
  }
  if (term.type === "exclusive") {
    let changed = false;
    const terms: Usage[] = [];
    for (const group of term.terms) {
      const filtered = filterUsage(group, hidden);
      if (filtered !== group) changed = true;
      if (filtered.length > 0) terms.push(filtered);
      else changed = true;
    }
    if (!changed) return term;
    if (terms.length === 0) return undefined;
    return { type: "exclusive", terms };
  }
  return term;
}

function filterUsage(usage: Usage, hidden: ReadonlySet<string>): Usage {
  if (hidden.size === 0) return usage;
  let changed = false;
  const next: UsageTerm[] = [];
  for (const term of usage) {
    const filtered = filterTerm(term, hidden);
    if (filtered !== term) changed = true;
    if (filtered != null) next.push(filtered);
  }
  return changed ? next : usage;
}

/**
 * Removes options whose dependencies are unsatisfied and not required.
 * Metadata is read from usage terms so wrapped options stay correct.
 *
 * @param usage Usage to filter.
 * @param catalog Sibling fields.
 * @param state Current object state.
 * @returns Usage with conditionally hidden options removed.
 * @since 0.10.0
 */
export function hideUnsatisfiedOptions(
  usage: Usage,
  catalog: readonly DependencyField[],
  state: unknown,
): Usage {
  return filterUsage(usage, hiddenFlags(catalog, state));
}

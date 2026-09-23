import {
  isDeferredParseState,
  isDependencySourceState,
  isPendingDependencySourceState,
} from "./dependency.ts";
import { type Message, message, text } from "./message.ts";
import type { DependsOn, OptionDependency, Usage, UsageTerm } from "./usage.ts";

/**
 * How an option's current state satisfies a dependency check.
 * @internal
 */
export interface OptionRead {
  /**
   * `true` when the user explicitly provided the option.
   */
  readonly provided: boolean;

  /**
   * Parsed value, or `undefined` when the option was not provided.
   */
  readonly value: unknown;

  /**
   * `true` when the user provided a falsy value such as `--flag=false`.
   */
  readonly explicitFalsy: boolean;
}

/**
 * One object field that can be referenced by `dependsOn`.
 * @internal
 */
interface OptionSlot {
  readonly key: string;
  readonly names: readonly string[];
  readonly acceptsValue: boolean;
  readonly state: unknown;
}

/**
 * Resolved option values keyed by object key and by CLI flag.
 * @internal
 */
export interface DependencyCatalog {
  readonly byRef: ReadonlyMap<string, OptionSlot>;
}

const absent: OptionRead = {
  provided: false,
  value: undefined,
  explicitFalsy: false,
};

/**
 * A condition accepted by {@link requiredWhen}, {@link optionalWhen},
 * and {@link conditionalOption}.
 *
 * Strings name an object key or CLI flag. Objects are either a single
 * `{ option, value }` dependency or a full {@link DependsOn} configuration.
 *
 * @since 0.10.0
 */
export type DependencyCondition = string | OptionDependency | DependsOn;

/**
 * Normalizes a helper condition into a {@link DependsOn} value.
 *
 * @param condition String key or flag, a single dependency, or a full
 *                  `dependsOn` configuration.
 * @param required When set, overrides `dependsOn.required`.
 * @returns A dependency configuration.
 * @since 0.10.0
 */
export function normalizeDependsOn(
  condition: DependencyCondition,
  required?: boolean,
): DependsOn {
  const base: DependsOn = typeof condition === "string"
    ? { option: condition }
    : { ...condition };
  if (required === undefined) return base;
  return { ...base, required };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Collects option usage terms nested under wrappers.
 * @internal
 */
export function collectOptionTerms(
  usage: Usage | undefined,
): readonly (Extract<UsageTerm, { type: "option" }>)[] {
  if (usage == null) return [];
  const found: Extract<UsageTerm, { type: "option" }>[] = [];
  const visit = (terms: Usage | undefined): void => {
    if (terms == null) return;
    for (const term of terms) {
      if (term == null) continue;
      if (term.type === "option") {
        found.push(term);
      } else if (term.type === "optional" || term.type === "multiple") {
        visit(term.terms);
      } else if (term.type === "exclusive") {
        for (const group of term.terms) visit(group);
      }
    }
  };
  visit(usage);
  return found;
}

/**
 * Returns the field's own option term when the parser is a single option
 * (possibly wrapped). Composite parsers such as nested `object()` calls are
 * ignored so each link is evaluated by the object that owns it.
 * @internal
 */
export function soleOptionTerm(
  usage: Usage | undefined,
): Extract<UsageTerm, { type: "option" }> | undefined {
  const terms = collectOptionTerms(usage);
  if (terms.length !== 1) return undefined;
  return terms[0];
}

function hasValueConstraint(
  dependency: { readonly value?: unknown },
): boolean {
  return Object.prototype.hasOwnProperty.call(dependency, "value") &&
    dependency.value !== undefined;
}

/**
 * Reads an option value from a parser state.
 *
 * Accepts value-parser results, dependency-source wrappers, single-element
 * wrapper arrays (`optional` / `withDefault`), and plain values. Does not
 * call `complete`.
 *
 * @param state Field state stored by the parser.
 * @param acceptsValue `true` when the option takes a value (has a metavar).
 *                     Boolean flags cannot be explicitly false.
 * @returns Whether the option was provided and the value to compare.
 * @internal
 */
export function readOptionState(
  state: unknown,
  acceptsValue: boolean,
): OptionRead {
  if (Array.isArray(state)) {
    if (state.length === 0) return absent;
    if (state.length === 1) return readOptionState(state[0], acceptsValue);
    let chosen = absent;
    for (const item of state) {
      const read = readOptionState(item, acceptsValue);
      if (read.provided) chosen = read;
    }
    return chosen;
  }

  if (state == null) return absent;
  if (isPendingDependencySourceState(state)) return absent;
  if (isDependencySourceState(state)) {
    return readOptionState(state.result, acceptsValue);
  }
  if (isDeferredParseState(state)) {
    return readOptionState(state.preliminaryResult, acceptsValue);
  }

  if (isRecord(state) && typeof state.success === "boolean") {
    if (!state.success) return absent;
    return interpretValue(state.value, acceptsValue);
  }

  return interpretValue(state, acceptsValue);
}

function interpretValue(value: unknown, acceptsValue: boolean): OptionRead {
  // Boolean flags start as `{ success: true, value: false }` and cannot
  // take an explicit false value (`--flag=false` is rejected).
  if (!acceptsValue && value === false) {
    return { provided: false, value: false, explicitFalsy: false };
  }
  if (!acceptsValue && value === true) {
    return { provided: true, value: true, explicitFalsy: false };
  }
  const explicitFalsy = value === false || value === 0 || value === "" ||
    (typeof value === "number" && Number.isNaN(value));
  return {
    provided: true,
    value,
    explicitFalsy,
  };
}

function slotFor(
  catalog: DependencyCatalog,
  ref: string,
): OptionSlot | undefined {
  return catalog.byRef.get(ref);
}

function userFacingFlag(slot: OptionSlot | undefined, ref: string): string {
  if (slot != null) {
    const long = slot.names.find((name) => name.startsWith("--"));
    if (long != null) return long;
    if (slot.names.length > 0) return slot.names[0];
  }
  return ref;
}

interface EvaluatedDependency {
  readonly satisfied: boolean;
  readonly flag: string;
  readonly read: OptionRead;
  readonly expected: unknown;
  readonly hasExpected: boolean;
}

function evaluateOne(
  dependency: OptionDependency,
  catalog: DependencyCatalog,
): EvaluatedDependency {
  const slot = slotFor(catalog, dependency.option);
  const read = slot == null
    ? absent
    : readOptionState(slot.state, slot.acceptsValue);
  const hasExpected = hasValueConstraint(dependency);
  const satisfied = slot == null
    ? false
    : hasExpected
    ? read.value === dependency.value
    : Boolean(read.value);
  return {
    satisfied,
    flag: userFacingFlag(slot, dependency.option),
    read,
    expected: dependency.value,
    hasExpected,
  };
}

function singleDependencies(
  dependsOn: DependsOn,
): readonly OptionDependency[] {
  if (dependsOn.option == null) return [];
  const dependency: OptionDependency = hasValueConstraint(dependsOn)
    ? { option: dependsOn.option, value: dependsOn.value }
    : { option: dependsOn.option };
  return [dependency];
}

/**
 * Whether a dependency configuration is satisfied by the catalog.
 *
 * Missing keys and flags are unsatisfied. Empty `allOf` is satisfied.
 * Empty `anyOf` is unsatisfied. Each listed dependency is evaluated on its
 * own; transitive links are not collapsed.
 *
 * @internal
 */
export function dependencySatisfied(
  dependsOn: DependsOn,
  catalog: DependencyCatalog,
): boolean {
  for (const dependency of singleDependencies(dependsOn)) {
    if (!evaluateOne(dependency, catalog).satisfied) return false;
  }
  if (dependsOn.allOf != null) {
    for (const dependency of dependsOn.allOf) {
      if (!evaluateOne(dependency, catalog).satisfied) return false;
    }
  }
  if (dependsOn.anyOf != null) {
    if (dependsOn.anyOf.length === 0) return false;
    const anySatisfied = dependsOn.anyOf.some((dependency) =>
      evaluateOne(dependency, catalog).satisfied
    );
    if (!anySatisfied) return false;
  }
  return true;
}

function unsatisfiedEvaluations(
  dependsOn: DependsOn,
  catalog: DependencyCatalog,
): readonly EvaluatedDependency[] {
  const failed: EvaluatedDependency[] = [];
  for (const dependency of singleDependencies(dependsOn)) {
    const evaluated = evaluateOne(dependency, catalog);
    if (!evaluated.satisfied) failed.push(evaluated);
  }
  if (dependsOn.allOf != null) {
    for (const dependency of dependsOn.allOf) {
      const evaluated = evaluateOne(dependency, catalog);
      if (!evaluated.satisfied) failed.push(evaluated);
    }
  }
  if (dependsOn.anyOf != null) {
    const evaluated = dependsOn.anyOf.map((dependency) =>
      evaluateOne(dependency, catalog)
    );
    if (
      dependsOn.anyOf.length === 0 ||
      evaluated.every((item) => !item.satisfied)
    ) {
      failed.push(...evaluated);
    }
  }
  return failed;
}

/**
 * `true` when an unsatisfied dependency was caused by an explicitly falsy
 * value rather than a missing option.
 * @internal
 */
export function dependencyBlockedByExplicitFalsy(
  dependsOn: DependsOn,
  catalog: DependencyCatalog,
): boolean {
  for (const dependency of singleDependencies(dependsOn)) {
    const evaluated = evaluateOne(dependency, catalog);
    if (!evaluated.satisfied && evaluated.read.explicitFalsy) return true;
  }
  if (dependsOn.allOf != null) {
    for (const dependency of dependsOn.allOf) {
      const evaluated = evaluateOne(dependency, catalog);
      if (!evaluated.satisfied && evaluated.read.explicitFalsy) return true;
    }
  }
  if (dependsOn.anyOf != null && dependsOn.anyOf.length > 0) {
    const evaluated = dependsOn.anyOf.map((dependency) =>
      evaluateOne(dependency, catalog)
    );
    if (
      evaluated.every((item) => !item.satisfied) &&
      evaluated.every((item) => item.read.explicitFalsy)
    ) {
      return true;
    }
  }
  return false;
}

function requiresMessage(
  dependentFlag: string,
  failures: readonly EvaluatedDependency[],
): Message {
  const parts: string[] = [];
  const seen = new Set<string>();
  for (const failure of failures) {
    const piece = failure.hasExpected
      ? `${failure.flag} ${String(failure.expected)}`
      : failure.flag;
    if (seen.has(piece)) continue;
    seen.add(piece);
    parts.push(piece);
  }
  const target = parts.length > 0 ? parts.join(", ") : "";
  if (target === "") {
    return message`${text(dependentFlag)} requires option.`;
  }
  return message`${text(dependentFlag)} requires option ${text(target)}.`;
}

/**
 * Catalog of option fields used to resolve `dependsOn` references.
 *
 * `pairs` entries with a missing parser are skipped. This function never
 * calls `complete`.
 *
 * @internal
 */
export function catalogFromFields(
  fields: readonly (readonly [
    PropertyKey,
    { readonly usage?: Usage } | undefined,
  ])[],
  state: unknown,
): DependencyCatalog {
  const record = isRecord(state) ? state : undefined;
  const byRef = new Map<string, OptionSlot>();
  for (const [key, parser] of fields) {
    if (parser == null || parser.usage == null) continue;
    // Composite parsers (nested object/tuple) own their dependencies.
    const term = soleOptionTerm(parser.usage);
    if (term == null) continue;
    const keyString = typeof key === "string"
      ? key
      : typeof key === "number"
      ? String(key)
      : undefined;
    const fieldState = keyString != null && record != null &&
        Object.prototype.hasOwnProperty.call(record, keyString)
      ? record[keyString]
      : undefined;
    const slot: OptionSlot = {
      key: keyString ?? term.names[0] ?? "",
      names: term.names,
      acceptsValue: term.metavar != null,
      state: fieldState,
    };
    if (keyString != null) byRef.set(keyString, slot);
    for (const name of term.names) byRef.set(name, slot);
  }
  return { byRef };
}

/**
 * Builds a catalog from usage terms tagged with `fieldKey`.
 * @internal
 */
export function catalogFromUsage(
  usage: Usage,
  state: unknown,
): DependencyCatalog {
  const record = isRecord(state) ? state : undefined;
  const byRef = new Map<string, OptionSlot>();
  for (const term of collectOptionTerms(usage)) {
    if (term.fieldKey == null) continue;
    const fieldState = record != null &&
        Object.prototype.hasOwnProperty.call(record, term.fieldKey)
      ? record[term.fieldKey]
      : undefined;
    const slot: OptionSlot = {
      key: term.fieldKey,
      names: term.names,
      acceptsValue: term.metavar != null,
      state: fieldState,
    };
    const existing = byRef.get(term.fieldKey);
    if (existing == null) byRef.set(term.fieldKey, slot);
    for (const name of term.names) {
      if (!byRef.has(name)) byRef.set(name, slot);
    }
  }
  return { byRef };
}

function dependentFlag(
  usage: Usage | undefined,
  fallback: string,
): string {
  const term = soleOptionTerm(usage);
  if (term == null) return fallback;
  return userFacingFlag(
    {
      key: fallback,
      names: term.names,
      acceptsValue: term.metavar != null,
      state: undefined,
    },
    fallback,
  );
}

/**
 * Validation error for options whose dependency is not satisfied.
 *
 * Returns `undefined` when every provided option's dependency holds.
 * Does not call `complete` on any parser.
 *
 * @internal
 */
export function objectDependencyError(
  fields: readonly (readonly [
    PropertyKey,
    { readonly usage?: Usage } | undefined,
  ])[],
  state: unknown,
): Message | undefined {
  const catalog = catalogFromFields(fields, state);
  const record = isRecord(state) ? state : undefined;
  for (const [key, parser] of fields) {
    if (parser == null || parser.usage == null) continue;
    const term = soleOptionTerm(parser.usage);
    const dependsOn = term?.dependsOn;
    if (dependsOn == null) continue;
    const keyString = typeof key === "string"
      ? key
      : typeof key === "number"
      ? String(key)
      : undefined;
    const fieldState = keyString != null && record != null &&
        Object.prototype.hasOwnProperty.call(record, keyString)
      ? record[keyString]
      : undefined;
    const read = readOptionState(fieldState, term?.metavar != null);
    if (!read.provided) continue;
    if (dependencySatisfied(dependsOn, catalog)) continue;
    const required = dependsOn.required === true;
    const falsy = dependencyBlockedByExplicitFalsy(dependsOn, catalog);
    if (!required && !falsy) continue;
    const failures = unsatisfiedEvaluations(dependsOn, catalog);
    return requiresMessage(
      dependentFlag(parser.usage, keyString ?? "option"),
      failures,
    );
  }
  return undefined;
}

/**
 * Whether an option should be omitted from help and completion because its
 * dependency is unsatisfied and not required.
 *
 * Metadata is read from the usage term so wrapped parsers keep it.
 * @internal
 */
export function optionHiddenByDependency(
  usage: Usage | undefined,
  catalog: DependencyCatalog,
): boolean {
  if (usage == null) return false;
  const term = soleOptionTerm(usage);
  const dependsOn = term?.dependsOn;
  if (dependsOn == null) return false;
  if (dependsOn.required === true) return false;
  return !dependencySatisfied(dependsOn, catalog);
}

function usageHasDependsOn(usage: Usage | undefined): boolean {
  return collectOptionTerms(usage).some((term) => term.dependsOn != null);
}

function hideTerm(
  term: Extract<UsageTerm, { type: "option" }>,
  catalog: DependencyCatalog,
  state: unknown,
): boolean {
  if (term.dependsOn == null || term.dependsOn.required === true) return false;
  if (dependencySatisfied(term.dependsOn, catalog)) return false;
  // Only hide when the owning field is actually in this state. Nested
  // commands keep a different state shape and stay untouched.
  if (term.fieldKey == null) return true;
  if (!isRecord(state)) return false;
  return Object.prototype.hasOwnProperty.call(state, term.fieldKey);
}

function filterUsageTerms(
  usage: Usage,
  catalog: DependencyCatalog,
  state: unknown,
): Usage {
  const filtered: UsageTerm[] = [];
  for (const term of usage) {
    if (term.type === "option") {
      if (!hideTerm(term, catalog, state)) filtered.push(term);
      continue;
    }
    if (term.type === "optional") {
      const terms = filterUsageTerms(term.terms, catalog, state);
      if (terms.length === 0) continue;
      filtered.push({ type: "optional", terms });
      continue;
    }
    if (term.type === "multiple") {
      const terms = filterUsageTerms(term.terms, catalog, state);
      if (terms.length === 0) continue;
      filtered.push({ type: "multiple", terms, min: term.min });
      continue;
    }
    if (term.type === "exclusive") {
      const terms = term.terms
        .map((group) => filterUsageTerms(group, catalog, state))
        .filter((group) => group.length > 0);
      if (terms.length === 0) continue;
      filtered.push({ type: "exclusive", terms });
      continue;
    }
    filtered.push(term);
  }
  return filtered;
}

/**
 * Drops options whose non-required dependency is unsatisfied.
 *
 * Returns the original usage when nothing declares `dependsOn`.
 *
 * @param usage Usage description, including terms tagged with `fieldKey`.
 * @param state Parser state keyed by object field.
 * @returns Usage safe to show in help text.
 * @internal
 */
export function applyDependencyVisibility(
  usage: Usage,
  state: unknown,
): Usage {
  if (!usageHasDependsOn(usage)) return usage;
  const catalog = catalogFromUsage(usage, state);
  return filterUsageTerms(usage, catalog, state);
}

/**
 * Tags option terms with the object field that owns them.
 * Existing field keys (from a nested `object()`) are preserved.
 *
 * @internal
 */
export function annotateUsageField(usage: Usage, fieldKey: string): Usage {
  return usage.map((term) => annotateTerm(term, fieldKey));
}

function annotateTerm(term: UsageTerm, fieldKey: string): UsageTerm {
  if (term.type === "option") {
    if (term.fieldKey != null) return term;
    return { ...term, fieldKey };
  }
  if (term.type === "optional") {
    return {
      type: "optional",
      terms: annotateUsageField(term.terms, fieldKey),
    };
  }
  if (term.type === "multiple") {
    return {
      type: "multiple",
      terms: annotateUsageField(term.terms, fieldKey),
      min: term.min,
    };
  }
  if (term.type === "exclusive") {
    return {
      type: "exclusive",
      terms: term.terms.map((group) => annotateUsageField(group, fieldKey)),
    };
  }
  return term;
}

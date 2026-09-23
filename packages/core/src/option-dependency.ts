import type { Message } from "./message.ts";
import { text } from "./message.ts";
import type { Usage, UsageTerm } from "./usage.ts";

/**
 * A single option dependency.
 *
 * When {@link value} is set, the dependency is satisfied only when the
 * referenced option's value is strictly equal to {@link value}. When
 * {@link value} is omitted, the dependency is satisfied only when the
 * referenced option's value is truthy.
 *
 * @since 0.11.0
 */
export interface OptionDependency {
  /**
   * Object key produced by `object({ ... })`, or a CLI flag such as
   * `"--verbose"`. Flag strings are resolved to object keys from usage.
   */
  readonly option: string;

  /**
   * Expected value. Omit to require a truthy value.
   */
  readonly value?: unknown;
}

/**
 * Configuration for {@link OptionOptions.dependsOn}.
 *
 * A single dependency sets {@link option}. Compound dependencies set
 * {@link anyOf} and/or {@link allOf}. An empty `allOf` list is satisfied.
 * An empty `anyOf` list is not.
 *
 * @since 0.11.0
 */
export interface DependsOn {
  /**
   * Object key or CLI flag this option depends on.
   */
  readonly option?: string;

  /**
   * Expected value for {@link option}. Omit to require a truthy value.
   */
  readonly value?: unknown;

  /**
   * Satisfied when every condition is satisfied. An empty list is satisfied.
   */
  readonly allOf?: readonly OptionDependency[];

  /**
   * Satisfied when at least one condition is satisfied. An empty list is not.
   */
  readonly anyOf?: readonly OptionDependency[];

  /**
   * When `true`, an unsatisfied dependency is a validation error.
   * When omitted or `false`, the dependent option is hidden until the
   * dependency is satisfied, but parsing still accepts it unless a dependee
   * was explicitly set to a falsy value.
   */
  readonly required?: boolean;
}

/**
 * A condition accepted by {@link requiredWhen}, {@link optionalWhen},
 * and {@link conditionalOption}.
 *
 * @since 0.11.0
 */
export type DependencyCondition = string | DependsOn;

/**
 * How a referenced option was observed in parser state.
 * @internal
 */
export interface ResolvedOptionValue {
  /**
   * `false` when the option key or flag is not part of the parser.
   */
  readonly found: boolean;

  /**
   * `true` when the user explicitly provided the option.
   */
  readonly explicit: boolean;

  /**
   * Parsed or plain value, when one is available.
   */
  readonly value: unknown;

  /**
   * `true` when {@link value} was produced by the parser or a plain state.
   */
  readonly hasValue: boolean;
}

/**
 * Maps CLI flags to object keys using usage terms.
 * @internal
 */
export interface OptionCatalog {
  readonly flagToKey: ReadonlyMap<string, string>;
  readonly keyToFlags: ReadonlyMap<string, readonly string[]>;
  readonly keyToDependsOn: ReadonlyMap<string, DependsOn>;
}

interface OptionTermInfo {
  readonly type: "option";
  readonly names: readonly string[];
  readonly hidden?: boolean;
  readonly dependsOn?: DependsOn;
  readonly ownerKey?: string;
  readonly dependencyMet?: boolean;
}

/**
 * Reads dependency metadata stored on a usage term.
 *
 * Wrappers such as `withDefault` keep the inner parser's usage, so callers
 * must use this instead of looking only at the wrapper instance.
 *
 * @param usage Usage tree to search.
 * @returns The first `dependsOn` configuration found, if any.
 * @since 0.11.0
 */
export function findDependsOn(usage: Usage): DependsOn | undefined {
  return findDependsOnTerm(usage)?.dependsOn;
}

function findDependsOnTerm(usage: Usage): OptionTermInfo | undefined {
  return walkOptions(usage).find((term) => term.dependsOn != null);
}

function walkOptions(usage: Usage): OptionTermInfo[] {
  const found: OptionTermInfo[] = [];
  const visit = (terms: Usage): void => {
    if (!terms || !Array.isArray(terms)) return;
    for (const term of terms) {
      if (term.type === "option") {
        found.push(term as OptionTermInfo);
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
 * Builds a flag-to-key catalog from usage terms.
 *
 * Option terms should carry `ownerKey` (stamped by `object()`). Flag names
 * are then mapped to that key so `dependsOn.option` can name either one.
 *
 * @param usage Usage tree, including wrapped parsers' terms.
 * @returns Catalog of flags, keys, and dependency metadata.
 * @internal
 */
export function catalogFromUsage(usage: Usage): OptionCatalog {
  const flagToKey = new Map<string, string>();
  const keyToFlags = new Map<string, string[]>();
  const keyToDependsOn = new Map<string, DependsOn>();

  for (const term of walkOptions(usage)) {
    const key = term.ownerKey ?? term.names[0] ?? "";
    if (key === "") continue;
    const flags = keyToFlags.get(key) ?? [];
    for (const name of term.names) {
      flagToKey.set(name, key);
      if (!flags.includes(name)) flags.push(name);
    }
    keyToFlags.set(key, flags);
    if (term.dependsOn) keyToDependsOn.set(key, term.dependsOn);
  }

  return { flagToKey, keyToFlags, keyToDependsOn };
}

/**
 * Stamps each option term with the object field that owns it.
 * @internal
 */
export function stampOwnerKey(usage: Usage, key: string): Usage {
  return usage.map((term) => stampTerm(term, key));
}

function stampTerm(term: UsageTerm, key: string): UsageTerm {
  if (term.type === "option") {
    return { ...term, ownerKey: key };
  }
  if (term.type === "optional") {
    return { type: "optional", terms: stampOwnerKey(term.terms, key) };
  }
  if (term.type === "multiple") {
    return {
      type: "multiple",
      terms: stampOwnerKey(term.terms, key),
      min: term.min,
    };
  }
  if (term.type === "exclusive") {
    return {
      type: "exclusive",
      terms: term.terms.map((group) => stampOwnerKey(group, key)),
    };
  }
  return term;
}

/**
 * Marks option terms whose dependencies are currently satisfied so help
 * formatting can keep them visible.
 * @internal
 */
export function applyDependencyMet(
  usage: Usage,
  state: unknown,
  initialState?: unknown,
): Usage {
  const catalog = catalogFromUsage(usage);
  const satisfiedKeys = new Set<string>();
  for (const [key, dependsOn] of catalog.keyToDependsOn) {
    if (
      isDependsOnSatisfied(dependsOn, state, catalog, initialState)
    ) {
      satisfiedKeys.add(key);
    }
  }
  const marked = mapUsage(usage, (term) => {
    if (term.type !== "option") return term;
    const info = term as OptionTermInfo;
    if (!info.dependsOn || info.ownerKey == null) return term;
    if (!satisfiedKeys.has(info.ownerKey)) return term;
    return { ...term, dependencyMet: true };
  });
  return pruneHiddenDependencies(marked);
}

function pruneHiddenDependencies(usage: Usage): Usage {
  const kept: UsageTerm[] = [];
  for (const term of usage) {
    if (term.type === "option") {
      if (!isConditionallyHidden(term)) kept.push(term);
      continue;
    }
    if (term.type === "optional" || term.type === "multiple") {
      const terms = pruneHiddenDependencies(term.terms);
      if (terms.length === 0) continue;
      kept.push(term.type === "optional"
        ? { type: "optional", terms }
        : { type: "multiple", terms, min: term.min });
      continue;
    }
    if (term.type === "exclusive") {
      const groups = term.terms
        .map((group) => pruneHiddenDependencies(group))
        .filter((group) => group.length > 0);
      if (groups.length === 0) continue;
      kept.push({ type: "exclusive", terms: groups });
      continue;
    }
    kept.push(term);
  }
  return kept;
}

function mapUsage(
  usage: Usage,
  fn: (term: UsageTerm) => UsageTerm,
): Usage {
  return usage.map((term) => {
    if (term.type === "optional") {
      return { type: "optional", terms: mapUsage(term.terms, fn) };
    }
    if (term.type === "multiple") {
      return {
        type: "multiple",
        terms: mapUsage(term.terms, fn),
        min: term.min,
      };
    }
    if (term.type === "exclusive") {
      return {
        type: "exclusive",
        terms: term.terms.map((group) => mapUsage(group, fn)),
      };
    }
    return fn(term);
  });
}

/**
 * Whether a non-required dependency should hide the option.
 * @internal
 */
export function isConditionallyHidden(
  term: UsageTerm,
): boolean {
  if (term.type !== "option") return false;
  const info = term as OptionTermInfo;
  if (info.hidden) return true;
  if (!info.dependsOn) return false;
  if (info.dependsOn.required === true) return false;
  return info.dependencyMet !== true;
}

/**
 * User-facing flag for an object key or flag reference.
 * @internal
 */
export function userFacingOptionName(
  ref: string,
  catalog: OptionCatalog,
): string {
  if (isFlagName(ref)) return ref;
  const flags = catalog.keyToFlags.get(ref);
  if (flags == null || flags.length === 0) return ref;
  return flags.find((flag) => flag.startsWith("--")) ?? flags[0];
}

function isFlagName(ref: string): boolean {
  return ref.startsWith("-") || ref.startsWith("/") || ref.startsWith("+");
}

/**
 * Resolves `dependsOn.option` to an object key.
 * Unknown keys and flags stay unresolved so the dependency is unsatisfied.
 * @internal
 */
export function resolveOptionKey(
  ref: string,
  catalog: OptionCatalog,
): string | undefined {
  if (catalog.keyToFlags.has(ref)) return ref;
  const fromFlag = catalog.flagToKey.get(ref);
  if (fromFlag != null) return fromFlag;
  return undefined;
}

/**
 * Whether `dependsOn` is satisfied by `state`.
 *
 * `state` may be an object of parser states (including `withDefault`
 * wrappers) or an object of plain values.
 *
 * @internal
 */
export function isDependsOnSatisfied(
  dependsOn: DependsOn,
  state: unknown,
  catalog: OptionCatalog,
  initialState?: unknown,
): boolean {
  const checks: boolean[] = [];
  if (dependsOn.option != null) {
    const single: OptionDependency = hasOwnValue(dependsOn)
      ? { option: dependsOn.option, value: dependsOn.value }
      : { option: dependsOn.option };
    checks.push(
      isSingleSatisfied(single, dependsOn, state, catalog, initialState),
    );
  }
  if (dependsOn.allOf != null) {
    checks.push(
      dependsOn.allOf.every((condition) =>
        isSingleSatisfied(condition, dependsOn, state, catalog, initialState)
      ),
    );
  }
  if (dependsOn.anyOf != null) {
    checks.push(
      dependsOn.anyOf.some((condition) =>
        isSingleSatisfied(condition, dependsOn, state, catalog, initialState)
      ),
    );
  }
  if (checks.length === 0) return false;
  return checks.every((check) => check);
}

function hasOwnValue(condition: OptionDependency | DependsOn): boolean {
  return Object.prototype.hasOwnProperty.call(condition, "value");
}

function isSingleSatisfied(
  condition: OptionDependency,
  parent: DependsOn,
  state: unknown,
  catalog: OptionCatalog,
  initialState?: unknown,
): boolean {
  const valueConstraint = hasOwnValue(condition)
    ? condition.value
    : hasOwnValue(parent) && condition.option === parent.option
    ? parent.value
    : undefined;
  const constrained = hasOwnValue(condition) ||
    (hasOwnValue(parent) && condition.option === parent.option);
  const resolved = resolveOptionValue(
    condition.option,
    state,
    catalog,
    initialState,
  );
  if (!resolved.found || !resolved.hasValue) return false;
  if (constrained) return Object.is(resolved.value, valueConstraint);
  return Boolean(resolved.value);
}

/**
 * Reads one option from parser state or a plain state object.
 * @internal
 */
export function resolveOptionValue(
  ref: string,
  state: unknown,
  catalog: OptionCatalog,
  initialState?: unknown,
): ResolvedOptionValue {
  const key = resolveOptionKey(ref, catalog);
  if (key == null) {
    return { found: false, explicit: false, value: undefined, hasValue: false };
  }
  if (state == null || typeof state !== "object") {
    return { found: true, explicit: false, value: undefined, hasValue: false };
  }
  const record = state as Record<string, unknown>;
  if (!Object.prototype.hasOwnProperty.call(record, key)) {
    return { found: true, explicit: false, value: undefined, hasValue: false };
  }
  const initialRecord = initialState != null && typeof initialState === "object"
    ? initialState as Record<string, unknown>
    : undefined;
  const initial = initialRecord == null
    ? undefined
    : Object.prototype.hasOwnProperty.call(initialRecord, key)
    ? initialRecord[key]
    : undefined;
  return {
    found: true,
    ...interpretState(record[key], initial),
  };
}

function interpretState(
  state: unknown,
  initial: unknown,
): Pick<ResolvedOptionValue, "explicit" | "value" | "hasValue"> {
  if (state == null) {
    return { explicit: false, value: undefined, hasValue: false };
  }
  if (Array.isArray(state)) {
    if (state.length === 0) {
      return { explicit: false, value: undefined, hasValue: false };
    }
    return interpretState(state[0], undefined);
  }
  if (typeof state !== "object") {
    return { explicit: true, value: state, hasValue: true };
  }
  if (
    "implicit" in state &&
    (state as { implicit?: boolean }).implicit === true &&
    "value" in state
  ) {
    return {
      explicit: false,
      value: (state as { value: unknown }).value,
      hasValue: true,
    };
  }
  if (isPending(state)) {
    return { explicit: false, value: undefined, hasValue: false };
  }
  if (isDependencySource(state)) {
    const result = (state as { result: { success: boolean; value?: unknown } })
      .result;
    if (!result.success) {
      return { explicit: false, value: undefined, hasValue: false };
    }
    return { explicit: true, value: result.value, hasValue: true };
  }
  if (isDeferred(state)) {
    const preliminary = (state as {
      preliminaryResult: { success: boolean; value?: unknown };
    }).preliminaryResult;
    if (!preliminary.success) {
      return { explicit: true, value: undefined, hasValue: false };
    }
    return { explicit: true, value: preliminary.value, hasValue: true };
  }
  if ("success" in state) {
    const parsed = state as { success: boolean; value?: unknown };
    if (!parsed.success) {
      return { explicit: false, value: undefined, hasValue: false };
    }
    if (state === initial) {
      return { explicit: false, value: parsed.value, hasValue: true };
    }
    return { explicit: true, value: parsed.value, hasValue: true };
  }
  if ("value" in state && Object.keys(state).length <= 2) {
    return {
      explicit: true,
      value: (state as { value: unknown }).value,
      hasValue: true,
    };
  }
  return { explicit: true, value: state, hasValue: true };
}

function isPending(state: object): boolean {
  const marker = Symbol.for(
    "@optique/core/dependency/pendingDependencySourceStateMarker",
  );
  return marker in state &&
    (state as Record<symbol, unknown>)[marker] === true;
}

function isDependencySource(state: object): boolean {
  const marker = Symbol.for(
    "@optique/core/dependency/dependencySourceStateMarker",
  );
  return marker in state &&
    (state as Record<symbol, unknown>)[marker] === true;
}

function isDeferred(state: object): boolean {
  const marker = Symbol.for(
    "@optique/core/dependency/deferredParseMarker",
  );
  return marker in state &&
    (state as Record<symbol, unknown>)[marker] === true;
}

/**
 * Runs `complete` only when both the parser and the state exist.
 *
 * Dependency checks must not call completion on a missing parser or with
 * `undefined` state (for example a `withDefault` field that was not set).
 *
 * @param parser Parser that may be missing when a dependency names an unknown key.
 * @param state Field state. `undefined` means the option was not parsed.
 * @returns The completed value, or `undefined` when completion is skipped.
 * @internal
 */
export function completeIfPresent(
  parser: { complete: (state: unknown) => unknown } | undefined,
  state: unknown,
): unknown {
  if (parser == null || state === undefined) return undefined;
  const result = parser.complete(state);
  if (
    result != null && typeof result === "object" && "success" in result &&
    (result as { success: boolean }).success
  ) {
    return (result as { value: unknown }).value;
  }
  return undefined;
}

/**
 * Validation error when a required dependency is unsatisfied, or when the
 * user supplies a dependent option while a dependee was explicitly falsy.
 *
 * @returns A message containing the substring `"requires option"` and the
 *          dependee's CLI flag, plus the expected value when one was set.
 *          `undefined` when the dependency does not fail validation.
 * @internal
 */
export function dependencyValidationError(
  dependentKey: string,
  dependsOn: DependsOn,
  rawState: unknown,
  catalog: OptionCatalog,
  initialState: unknown | undefined,
  dependentExplicit: boolean,
): Message | undefined {
  const satisfied = isDependsOnSatisfied(
    dependsOn,
    rawState,
    catalog,
    initialState,
  );
  if (satisfied) return undefined;

  const required = dependsOn.required === true;
  const explicitFalsy = dependentExplicit &&
    hasExplicitFalsyDependee(dependsOn, rawState, catalog, initialState);
  if (!required && !explicitFalsy) return undefined;

  const parts = unmetConditions(dependsOn);
  const flags = parts.map((part) => userFacingOptionName(part.option, catalog));
  const expected = parts
    .filter((part) => hasOwnValue(part))
    .map((part) => String(part.value));
  const dependent = userFacingOptionName(dependentKey, catalog);
  const flagText = flags.length > 0 ? flags.join(", ") : dependsOn.option ?? "";
  const expectedText = expected.length > 0 ? ` ${expected.join(", ")}` : "";
  return [text(
    `Option ${dependent} requires option ${flagText}${expectedText}.`,
  )];
}

function unmetConditions(dependsOn: DependsOn): OptionDependency[] {
  const parts: OptionDependency[] = [];
  if (dependsOn.option != null) {
    parts.push(
      hasOwnValue(dependsOn)
        ? { option: dependsOn.option, value: dependsOn.value }
        : { option: dependsOn.option },
    );
  }
  if (dependsOn.allOf != null) parts.push(...dependsOn.allOf);
  if (dependsOn.anyOf != null && dependsOn.anyOf.length > 0) {
    parts.push(dependsOn.anyOf[0]);
  }
  return parts.filter((part) => part.option != null);
}

function hasExplicitFalsyDependee(
  dependsOn: DependsOn,
  state: unknown,
  catalog: OptionCatalog,
  initialState?: unknown,
): boolean {
  const refs: OptionDependency[] = [];
  if (dependsOn.option != null) {
    refs.push({ option: dependsOn.option, value: dependsOn.value });
  }
  if (dependsOn.allOf) refs.push(...dependsOn.allOf);
  if (dependsOn.anyOf) refs.push(...dependsOn.anyOf);
  return refs.some((ref) => {
    const resolved = resolveOptionValue(
      ref.option,
      state,
      catalog,
      initialState,
    );
    return resolved.explicit && resolved.hasValue && !resolved.value;
  });
}

/**
 * Whether the dependent field was explicitly provided.
 * @internal
 */
export function fieldWasExplicit(
  key: string,
  state: unknown,
  initialState?: unknown,
): boolean {
  if (state == null || typeof state !== "object") return false;
  const record = state as Record<string, unknown>;
  if (!Object.prototype.hasOwnProperty.call(record, key)) return false;
  const initialRecord = initialState != null && typeof initialState === "object"
    ? initialState as Record<string, unknown>
    : undefined;
  const initial = initialRecord == null ? undefined : initialRecord[key];
  return interpretState(record[key], initial).explicit;
}

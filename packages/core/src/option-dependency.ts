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
  text,
  value as valueTerm,
} from "./message.ts";
import type { Mode, Parser } from "./parser.ts";
import type { OptionName, Usage, UsageTerm } from "./usage.ts";

/**
 * A condition on a single option, used by {@link OptionDependsOn}.
 * @since 0.10.0
 */
export interface OptionDependencyCondition {
  /**
   * The option this condition refers to.  It can be either the key of
   * the field in the enclosing `object()` parser (e.g., `"auth"`) or one of
   * the option's CLI flags (e.g., `"--auth"`).
   */
  readonly option: string;

  /**
   * The value the referenced option must be equal to.  When omitted,
   * the condition is satisfied when the referenced option is present with
   * a truthy value.
   */
  readonly value?: unknown;
}

/**
 * A compound condition over multiple options, used by {@link OptionDependsOn}.
 * When both `anyOf` and `allOf` are given, both must be satisfied.
 * @since 0.10.0
 */
export interface OptionDependencyCompound {
  /**
   * Satisfied when at least one of the conditions is satisfied.
   * An empty array is never satisfied.
   */
  readonly anyOf?: readonly OptionDependencyConditionLike[];

  /**
   * Satisfied when all of the conditions are satisfied.
   * An empty array is always satisfied.
   */
  readonly allOf?: readonly OptionDependencyConditionLike[];
}

/**
 * Any form of an option dependency condition: an option reference as
 * a string (see {@link OptionDependencyCondition.option}), a single
 * condition object, or a compound condition.
 * @since 0.10.0
 */
export type OptionDependencyConditionLike =
  | string
  | OptionDependencyCondition
  | OptionDependencyCompound;

/**
 * Describes which other options an option depends on.  Dependencies are
 * evaluated by the enclosing `object()` parser.
 *
 * While the dependency is unsatisfied, the dependent option is hidden from
 * help text and shell completion.  If `required` is `true`, providing the
 * dependent option while the dependency is unsatisfied is a parse error;
 * otherwise, it is accepted unless a referenced option was explicitly given
 * a falsy value.
 * @since 0.10.0
 */
export type OptionDependsOn =
  & (OptionDependencyCondition | OptionDependencyCompound)
  & {
    /**
     * Whether the dependency must be satisfied for the dependent option
     * to be used.
     * @default `false`
     */
    readonly required?: boolean;
  };

/**
 * Marks the initial state of an option with `dependsOn` so that it can be
 * recognized as "not provided" even after the state has been copied
 * (e.g., by `merge()`).
 * @internal
 */
export const unprovidedOptionState: unique symbol = Symbol.for(
  "@optique/core/option/unprovided",
);

/**
 * Checks whether an option state is the untouched initial state of an option
 * with `dependsOn`.
 * @internal
 */
export function isUnprovidedOptionState(state: unknown): boolean {
  return typeof state === "object" && state != null &&
    unprovidedOptionState in state;
}

type OptionUsageTerm = Extract<UsageTerm, { readonly type: "option" }>;

interface DependencyTarget {
  readonly present: boolean;
  readonly value?: unknown;
  readonly displayName: string;
}

interface DependencyEvaluation {
  readonly satisfied: boolean;
  readonly explicitlyFalsy: boolean;
  readonly requirement: Message;
}

function isTruthy(value: unknown): boolean {
  if (typeof value === "string") {
    const normalized = value.trim().toLowerCase();
    return normalized !== "" && normalized !== "false" && normalized !== "0";
  }
  if (Array.isArray(value)) return value.length > 0;
  return Boolean(value);
}

function joinMessages(parts: readonly Message[], separator: string): Message {
  const terms: MessageTerm[] = [];
  parts.forEach((part, i) => {
    if (i > 0) terms.push(text(separator));
    terms.push(...part);
  });
  return terms;
}

function evaluateLeaf(
  ref: string,
  hasExpected: boolean,
  expected: unknown,
  lookup: (ref: string) => DependencyTarget,
): DependencyEvaluation {
  const target = lookup(ref);
  const satisfied = target.present &&
    (hasExpected ? target.value === expected : isTruthy(target.value));
  return {
    satisfied,
    explicitlyFalsy: !satisfied && target.present && !isTruthy(target.value),
    requirement: hasExpected
      ? message`option ${optionName(target.displayName)} to be ${
        valueTerm(String(expected))
      }`
      : message`option ${optionName(target.displayName)}`,
  };
}

function evaluateAllOf(
  evaluations: readonly DependencyEvaluation[],
): DependencyEvaluation {
  const unsatisfied = evaluations.filter((e) => !e.satisfied);
  return {
    satisfied: unsatisfied.length < 1,
    explicitlyFalsy: unsatisfied.some((e) => e.explicitlyFalsy),
    requirement: joinMessages(unsatisfied.map((e) => e.requirement), " and "),
  };
}

function evaluateAnyOf(
  evaluations: readonly DependencyEvaluation[],
): DependencyEvaluation {
  const satisfied = evaluations.some((e) => e.satisfied);
  return {
    satisfied,
    explicitlyFalsy: !satisfied && evaluations.some((e) => e.explicitlyFalsy),
    requirement: evaluations.length < 1
      ? message`option dependencies that cannot be satisfied`
      : joinMessages(evaluations.map((e) => e.requirement), " or "),
  };
}

function evaluateCondition(
  condition: OptionDependencyConditionLike,
  lookup: (ref: string) => DependencyTarget,
): DependencyEvaluation {
  if (typeof condition === "string") {
    return evaluateLeaf(condition, false, undefined, lookup);
  }
  const parts: DependencyEvaluation[] = [];
  if ("option" in condition && typeof condition.option === "string") {
    parts.push(
      evaluateLeaf(
        condition.option,
        condition.value !== undefined,
        condition.value,
        lookup,
      ),
    );
  }
  if ("anyOf" in condition && condition.anyOf != null) {
    parts.push(
      evaluateAnyOf(condition.anyOf.map((c) => evaluateCondition(c, lookup))),
    );
  }
  if ("allOf" in condition && condition.allOf != null) {
    parts.push(
      evaluateAllOf(condition.allOf.map((c) => evaluateCondition(c, lookup))),
    );
  }
  return parts.length === 1 ? parts[0] : evaluateAllOf(parts);
}

function collectOptionTerms(usage: Usage): OptionUsageTerm[] {
  const terms: OptionUsageTerm[] = [];
  const traverse = (usage: Usage): void => {
    for (const term of usage) {
      if (term.type === "option") terms.push(term);
      else if (term.type === "optional" || term.type === "multiple") {
        traverse(term.terms);
      } else if (term.type === "exclusive") {
        for (const branch of term.terms) traverse(branch);
      }
    }
  };
  traverse(usage);
  return terms;
}

function preferredName(names: readonly OptionName[]): string {
  return names.find((name) => name.startsWith("--")) ?? names[0];
}

/**
 * Checks whether a field state carries user input, as opposed to being the
 * parser's untouched initial state.
 */
function isProvidedState(state: unknown, initialState: unknown): boolean {
  if (state === undefined || state === initialState) return false;
  if (isUnprovidedOptionState(state)) return false;
  if (isPendingDependencySourceState(state)) return false;
  if (Array.isArray(state)) {
    return state.some((item) => isProvidedState(item, undefined));
  }
  return true;
}

const absent = { present: false } as const;

function readStateValue(
  state: unknown,
): { readonly present: boolean; readonly value?: unknown } {
  if (isDependencySourceState(state)) {
    return state.result.success
      ? { present: true, value: state.result.value }
      : absent;
  }
  if (isDeferredParseState(state)) {
    const result = state.preliminaryResult;
    return result.success ? { present: true, value: result.value } : absent;
  }
  if (Array.isArray(state)) {
    if (state.length === 1) return readStateValue(state[0]);
    const values = state.map(readStateValue).filter((r) => r.present);
    return values.length > 0
      ? { present: true, value: values.map((r) => r.value) }
      : absent;
  }
  if (
    typeof state === "object" && state != null && "success" in state &&
    typeof state.success === "boolean"
  ) {
    return state.success && "value" in state
      ? { present: true, value: state.value }
      : absent;
  }
  return absent;
}

function readFieldValue(
  parser: Parser<Mode, unknown, unknown> | undefined,
  state: unknown,
): { readonly present: boolean; readonly value?: unknown } {
  if (parser == null || !isProvidedState(state, parser.initialState)) {
    return absent;
  }
  if (parser.$mode === "sync") {
    const result = (parser as Parser<"sync", unknown, unknown>).complete(state);
    return result.success ? { present: true, value: result.value } : absent;
  }
  return readStateValue(state);
}

/**
 * The result of checking option dependencies of an `object()` parser's state.
 * @internal
 */
export interface OptionDependencyCheck {
  /**
   * The error to report, if a dependent option was provided although its
   * dependency is unsatisfied and must not be.
   */
  readonly error?: Message;

  /**
   * Fields whose dependent option was not provided and whose dependency is
   * unsatisfied.  These fields must not be reported as missing.
   */
  readonly inactive: ReadonlySet<string | symbol>;
}

/**
 * Evaluates `dependsOn` constraints among the fields of an `object()` parser.
 * @internal
 */
export interface ObjectOptionDependencies {
  /**
   * Returns the fields that must be hidden from help text and shell
   * completion for the given object state.
   */
  hiddenFields(state: unknown): ReadonlySet<string | symbol>;

  /**
   * Validates the dependencies against the given object state.
   */
  check(state: unknown): OptionDependencyCheck;
}

interface DependentField {
  readonly key: string | symbol;
  readonly name: string;
  readonly dependsOn: OptionDependsOn;
}

/**
 * Creates the dependency evaluator for the fields of an `object()` parser.
 * Dependency metadata is read from the fields' usage terms, so options
 * wrapped by modifiers such as `withDefault()` keep their dependencies.
 * @param fields The fields of the `object()` parser.
 * @returns The evaluator, or `undefined` if no field has dependencies.
 * @internal
 */
export function createObjectOptionDependencies(
  fields:
    readonly (readonly [string | symbol, Parser<Mode, unknown, unknown>])[],
): ObjectOptionDependencies | undefined {
  const parsers = new Map(fields);
  const flagToKey = new Map<string, string | symbol>();
  const displayNames = new Map<string | symbol, string>();
  const booleanFields = new Set<string | symbol>();
  const dependents: DependentField[] = [];
  for (const [key, parser] of fields) {
    const terms = collectOptionTerms(parser.usage);
    for (const term of terms) {
      for (const name of term.names) {
        if (!flagToKey.has(name)) flagToKey.set(name, key);
      }
    }
    if (terms.length > 0) {
      displayNames.set(key, preferredName(terms[0].names));
      if (terms[0].metavar == null) booleanFields.add(key);
    }
    const dependent = terms.find((term) => term.dependsOn != null);
    if (dependent?.dependsOn != null) {
      dependents.push({
        key,
        name: preferredName(dependent.names),
        dependsOn: dependent.dependsOn,
      });
    }
  }
  if (dependents.length < 1) return undefined;

  const fieldState = (state: unknown, key: string | symbol): unknown =>
    typeof state === "object" && state != null && key in state
      ? (state as Record<string | symbol, unknown>)[key]
      : parsers.get(key)?.initialState;

  const createLookup = (state: unknown) => (ref: string): DependencyTarget => {
    const key = parsers.has(ref) ? ref : flagToKey.get(ref);
    if (key === undefined) return { present: false, displayName: ref };
    const displayName = parsers.has(ref) ? displayNames.get(key) ?? ref : ref;
    const result = readFieldValue(parsers.get(key), fieldState(state, key));
    // A Boolean flag that is false is indistinguishable from an absent one:
    if (!result.present || booleanFields.has(key) && !isTruthy(result.value)) {
      return { present: false, displayName };
    }
    return { present: true, value: result.value, displayName };
  };

  const evaluate = (state: unknown) => {
    const lookup = createLookup(state);
    return dependents.map((dependent) => ({
      dependent,
      evaluation: evaluateCondition(dependent.dependsOn, lookup),
    }));
  };

  return {
    hiddenFields(state) {
      const hidden = new Set<string | symbol>();
      for (const { dependent, evaluation } of evaluate(state)) {
        if (!evaluation.satisfied && dependent.dependsOn.required !== true) {
          hidden.add(dependent.key);
        }
      }
      return hidden;
    },
    check(state) {
      const inactive = new Set<string | symbol>();
      for (const { dependent, evaluation } of evaluate(state)) {
        if (evaluation.satisfied) continue;
        const parser = parsers.get(dependent.key);
        if (
          !isProvidedState(
            fieldState(state, dependent.key),
            parser?.initialState,
          )
        ) {
          inactive.add(dependent.key);
          continue;
        }
        if (
          dependent.dependsOn.required === true || evaluation.explicitlyFalsy
        ) {
          return {
            error: message`${
              optionName(dependent.name)
            } requires ${evaluation.requirement}.`,
            inactive,
          };
        }
      }
      return { inactive };
    },
  };
}

/**
 * Removes option terms from a usage whose `dependsOn` is not required and
 * whose names are not listed in `visibleNames`.  Used to keep options hidden
 * by unsatisfied dependencies out of the usage line of help pages.
 * @internal
 */
export function removeHiddenDependentOptions(
  usage: Usage,
  visibleNames: ReadonlySet<string>,
): Usage {
  const result: UsageTerm[] = [];
  for (const term of usage) {
    if (term.type === "option") {
      if (
        term.dependsOn != null && term.dependsOn.required !== true &&
        !term.names.some((name) => visibleNames.has(name))
      ) {
        continue;
      }
      result.push(term);
    } else if (term.type === "optional" || term.type === "multiple") {
      const terms = removeHiddenDependentOptions(term.terms, visibleNames);
      if (terms.length > 0 || term.terms.length < 1) {
        result.push({ ...term, terms });
      }
    } else if (term.type === "exclusive") {
      const branches = term.terms
        .map((branch) => removeHiddenDependentOptions(branch, visibleNames))
        .filter((branch, i) => branch.length > 0 || term.terms[i].length < 1);
      if (branches.length > 0) result.push({ ...term, terms: branches });
    } else {
      result.push(term);
    }
  }
  return result;
}

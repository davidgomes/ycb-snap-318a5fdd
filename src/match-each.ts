import { Pattern } from './types/Pattern';
import { MatchEach } from './types/MatchEach';
import * as symbols from './internals/symbols';
import { matchPattern } from './internals/helpers';
import { NonExhaustiveError } from './errors';

type WithClause = {
  patterns: Pattern<unknown>[];
  predicate?: (value: unknown) => unknown;
  handler: (selection: unknown, value: unknown) => unknown;
};

type Step =
  | { type: 'with'; clause: WithClause }
  | {
      type: 'when';
      predicate: (value: unknown) => unknown;
      handler: (value: unknown) => unknown;
    }
  | { type: 'tap'; callback: (result: unknown) => unknown };

type InputState =
  | { hasValue: true; value: unknown }
  | { hasValue: false };

/**
 * `matchEach` evaluates every registered pattern against the input and
 * collects each matching handler's result, in declaration order.
 *
 * [Read the documentation for `match` on GitHub](https://github.com/gvergnaud/ts-pattern#match)
 *
 * @example
 *  return matchEach(input)
 *    .with(P.string, (s) => s.length)
 *    .with('hello', () => 0)
 *    .otherwise(() => []);
 */
export function matchEach<const input, output = symbols.unset>(
  value: input
): MatchEach<input, output>;
export function matchEach<input, output = symbols.unset>(): MatchEach<
  input,
  output
>;
export function matchEach(value?: unknown): MatchEach<unknown, unknown> {
  return new MatchEachExpression(
    arguments.length === 0
      ? { hasValue: false }
      : { hasValue: true, value },
    []
  ) as unknown as MatchEach<unknown, unknown>;
}

class MatchEachExpression {
  constructor(private inputState: InputState, private steps: Step[]) {}

  private clone(step: Step): MatchEachExpression {
    return new MatchEachExpression(this.inputState, [...this.steps, step]);
  }

  with(...args: any[]): MatchEachExpression {
    const handler: (selection: unknown, value: unknown) => unknown =
      args[args.length - 1];

    const patterns: Pattern<unknown>[] = [args[0]];
    let predicate: ((value: unknown) => unknown) | undefined = undefined;

    if (args.length === 3 && typeof args[1] === 'function') {
      predicate = args[1];
    } else if (args.length > 2) {
      patterns.push(...args.slice(1, args.length - 1));
    }

    return this.clone({
      type: 'with',
      clause: { patterns, predicate, handler },
    });
  }

  when(
    predicate: (value: unknown) => unknown,
    handler: (value: unknown) => unknown
  ): MatchEachExpression {
    return this.clone({ type: 'when', predicate, handler });
  }

  tap(callback: (result: unknown) => unknown): MatchEachExpression {
    return this.clone({ type: 'tap', callback });
  }

  otherwise(handler: (value: unknown) => unknown): unknown[] {
    return this.evaluate('otherwise', handler);
  }

  exhaustive(
    unexpectedValueHandler?: (value: unknown) => unknown
  ): unknown[] {
    return this.evaluate(
      unexpectedValueHandler ? 'fallback' : 'throw',
      unexpectedValueHandler
    );
  }

  run(): unknown[] {
    return this.exhaustive();
  }

  returnType(): this {
    return this;
  }

  narrow(): this {
    return this;
  }

  toFunction(): (input: unknown) => unknown[] {
    const steps = this.steps;
    return (input: unknown) =>
      evaluateSteps(steps, input, 'throw') as unknown[];
  }

  toExhaustiveFunction(): (input: unknown) => unknown[] {
    return this.toFunction();
  }

  toPartialFunction(): (input: unknown) => unknown[] | undefined {
    const steps = this.steps;
    return (input: unknown) => evaluateSteps(steps, input, 'partial');
  }

  private evaluate(
    mode: 'throw' | 'otherwise' | 'fallback',
    handler?: (value: unknown) => unknown
  ): unknown[] {
    return evaluateSteps(
      this.steps,
      this.readValue(),
      mode,
      handler
    ) as unknown[];
  }

  private readValue(): unknown {
    if (!this.inputState.hasValue) {
      throw new Error(
        'matchEach was built without an input value. Call toFunction(), toExhaustiveFunction(), or toPartialFunction() and pass the value to the compiled function.'
      );
    }
    return this.inputState.value;
  }
}

function evaluateSteps(
  steps: Step[],
  value: unknown,
  mode: 'throw' | 'otherwise' | 'fallback' | 'partial',
  unmatchedHandler?: (value: unknown) => unknown
): unknown[] | undefined {
  const results: unknown[] = [];

  for (const step of steps) {
    if (step.type === 'tap') {
      for (const result of results) step.callback(result);
      continue;
    }

    const matched = executeStep(step, value);
    if (matched.matched) results.push(matched.value);
  }

  if (results.length > 0) return results;

  if (mode === 'partial') return undefined;
  if (mode === 'otherwise' || mode === 'fallback') {
    return [unmatchedHandler!(value)];
  }
  throw new NonExhaustiveError(value);
}

function executeStep(
  step: Step,
  value: unknown
): { matched: true; value: unknown } | { matched: false } {
  if (step.type === 'tap') return { matched: false };

  if (step.type === 'when') {
    if (!step.predicate(value)) return { matched: false };
    return { matched: true, value: step.handler(value) };
  }

  const clause = step.clause;

  for (const pattern of clause.patterns) {
    let hasSelections = false;
    const selected: Record<string, unknown> = {};
    const select = (key: string, selectedValue: unknown) => {
      hasSelections = true;
      selected[key] = selectedValue;
    };

    if (!matchPattern(pattern, value, select)) continue;

    if (clause.predicate && !clause.predicate(value)) {
      // Guard applies to the whole clause (same as `match`).
      return { matched: false };
    }

    const selections = hasSelections
      ? symbols.anonymousSelectKey in selected
        ? selected[symbols.anonymousSelectKey]
        : selected
      : value;

    return { matched: true, value: clause.handler(selections, value) };
  }

  return { matched: false };
}

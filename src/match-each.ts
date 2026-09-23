import { MatchEach } from './types/MatchEach';
import * as symbols from './internals/symbols';
import { matchPattern } from './internals/helpers';
import { NonExhaustiveError } from './errors';

type Handler = (selections: unknown, value: unknown) => unknown;

type WithStep = {
  type: 'with';
  patterns: readonly unknown[];
  predicate?: (value: unknown) => unknown;
  handler: Handler;
};

type WhenStep = {
  type: 'when';
  predicate: (value: any) => unknown;
  handler: Handler;
};

type TapStep = {
  type: 'tap';
  callback: (result: any) => unknown;
};

type Step = WithStep | WhenStep | TapStep;

/**
 * `matchEach` creates a pattern matching expression that evaluates **every**
 * clause. Matching handlers all run, and their return values are collected
 * into an array in declaration order.
 *
 * Call it with a value to evaluate immediately, or with an explicit type
 * argument and no value to build a reusable function via `.toFunction()`,
 * `.toExhaustiveFunction()`, or `.toPartialFunction()`.
 *
 * @example
 *  declare let input: "A" | "B";
 *
 *  return matchEach(input)
 *    .with("A", () => "It's an A!")
 *    .with("B", () => "It's a B!")
 *    .with(P.string, (value) => value)
 *    .exhaustive();
 */
export function matchEach<input, output = symbols.unset>(): MatchEach<
  input,
  output
>;
export function matchEach<const input, output = symbols.unset>(
  value: input
): MatchEach<input, output>;
export function matchEach(
  ...args: [value?: unknown]
): MatchEach<unknown, symbols.unset> {
  return new MatchEachExpression(args.length > 0, args[0], []) as any;
}

/**
 * Builder for `matchEach`. Clauses are stored and evaluated together when
 * the expression is run or compiled, so every matching handler can fire.
 * Selection state is created per clause per evaluation.
 */
class MatchEachExpression<input, output> {
  constructor(
    private readonly hasValue: boolean,
    private readonly input: input,
    private readonly steps: readonly Step[]
  ) {}

  with(...args: any[]): MatchEachExpression<input, output> {
    const handler: Handler = args[args.length - 1];
    const patterns: unknown[] = [args[0]];
    let predicate: ((value: unknown) => unknown) | undefined = undefined;

    if (args.length === 3 && typeof args[1] === 'function') {
      predicate = args[1];
    } else if (args.length > 2) {
      patterns.push(...args.slice(1, args.length - 1));
    }

    return new MatchEachExpression(this.hasValue, this.input, [
      ...this.steps,
      { type: 'with', patterns, predicate, handler },
    ]);
  }

  when(
    predicate: (value: input) => unknown,
    handler: Handler
  ): MatchEachExpression<input, output> {
    return new MatchEachExpression(this.hasValue, this.input, [
      ...this.steps,
      { type: 'when', predicate, handler },
    ]);
  }

  tap(
    callback: (result: output) => unknown
  ): MatchEachExpression<input, output> {
    return new MatchEachExpression(this.hasValue, this.input, [
      ...this.steps,
      { type: 'tap', callback },
    ]);
  }

  otherwise(handler: (value: input) => output): output[] {
    const results = this.evaluate();
    if (results.length === 0) return [handler(this.input)];
    return results;
  }

  exhaustive(
    unexpectedValueHandler: (value: unknown) => output = defaultCatcher
  ): output[] {
    const results = this.evaluate();
    if (results.length === 0) return [unexpectedValueHandler(this.input)];
    return results;
  }

  run(): output[] {
    return this.exhaustive();
  }

  returnType(): this {
    return this;
  }

  narrow(): this {
    return this;
  }

  toFunction(): (input: input) => output[] {
    return compile(this.steps, 'throw');
  }

  toExhaustiveFunction(): (input: input) => output[] {
    return compile(this.steps, 'throw');
  }

  toPartialFunction(): (input: input) => output[] | undefined {
    return compile(this.steps, 'undefined');
  }

  private evaluate(): output[] {
    if (!this.hasValue) {
      throw new Error(
        'matchEach() was called without an input value. Use .toFunction(), .toExhaustiveFunction(), or .toPartialFunction().'
      );
    }
    return execute(this.steps, this.input);
  }
}

function compile<output>(
  steps: readonly Step[],
  onEmpty: 'throw'
): (input: unknown) => output[];
function compile<output>(
  steps: readonly Step[],
  onEmpty: 'undefined'
): (input: unknown) => output[] | undefined;
function compile<output>(
  steps: readonly Step[],
  onEmpty: 'throw' | 'undefined'
): (input: unknown) => output[] | undefined {
  return (input: unknown) => {
    const results = execute<output>(steps, input);
    if (results.length === 0) {
      if (onEmpty === 'undefined') return undefined;
      throw new NonExhaustiveError(input);
    }
    return results;
  };
}

function execute<output>(steps: readonly Step[], input: unknown): output[] {
  const results: output[] = [];

  for (const step of steps) {
    if (step.type === 'tap') {
      // Snapshot so a callback cannot change which results are visited.
      for (const result of results.slice()) {
        step.callback(result);
      }
      continue;
    }

    if (step.type === 'when') {
      if (Boolean(step.predicate(input))) {
        results.push(step.handler(input, input) as output);
      }
      continue;
    }

    const matched = matchWithStep(step, input);
    if (matched.matched) {
      results.push(step.handler(matched.selections, input) as output);
    }
  }

  return results;
}

function matchWithStep(
  step: WithStep,
  input: unknown
): { matched: true; selections: unknown } | { matched: false } {
  for (const pattern of step.patterns) {
    // Fresh state so a failed pattern cannot leak into a later one,
    // and so compiled functions do not reuse selections across calls.
    const selected: Record<string, unknown> = {};
    let hasSelections = false;
    const select = (key: string, value: unknown) => {
      hasSelections = true;
      selected[key] = value;
    };

    if (!matchPattern(pattern, input, select)) continue;

    if (step.predicate && !Boolean(step.predicate(input))) {
      return { matched: false };
    }

    return {
      matched: true,
      selections: readSelections(selected, hasSelections, input),
    };
  }

  return { matched: false };
}

function readSelections(
  selected: Record<string, unknown>,
  hasSelections: boolean,
  input: unknown
): unknown {
  if (!hasSelections) return input;
  if (symbols.anonymousSelectKey in selected) {
    return selected[symbols.anonymousSelectKey];
  }
  return selected;
}

function defaultCatcher(input: unknown): never {
  throw new NonExhaustiveError(input);
}

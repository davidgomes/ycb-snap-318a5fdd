import { Pattern } from './types/Pattern';
import { MatchEach } from './types/Match';
import * as symbols from './internals/symbols';
import { matchPattern } from './internals/helpers';
import { NonExhaustiveError } from './errors';

type InputSlot<input> = { provided: true; value: input } | { provided: false };

type WithStep<input, output> = {
  type: 'with';
  patterns: readonly Pattern<input>[];
  predicate?: (value: input) => unknown;
  handler: (selections: unknown, value: input) => output;
};

type WhenStep<input, output> = {
  type: 'when';
  predicate: (value: input) => unknown;
  handler: (value: input, value2: input) => output;
};

type TapStep<output> = {
  type: 'tap';
  callback: (result: output) => void;
};

type Step<input, output> =
  | WithStep<input, output>
  | WhenStep<input, output>
  | TapStep<output>;

const noInputMessage =
  'matchEach: no input value was provided. Pass a value to matchEach(value), or use .toFunction(), .toExhaustiveFunction(), or .toPartialFunction().';

/**
 * `matchEach` evaluates every registered clause and collects each matching
 * handler's result into an array, in declaration order.
 *
 * [Read the documentation for `match` on GitHub](https://github.com/gvergnaud/ts-pattern#match)
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
export function matchEach<const input, output = symbols.unset>(
  value: input
): MatchEach<input, output>;
export function matchEach<input, output = symbols.unset>(): MatchEach<
  input,
  output
>;
export function matchEach<input, output = symbols.unset>(
  value?: input
): MatchEach<input, output> {
  if (arguments.length === 0) {
    return new MatchEachExpression<input, output>(
      {
        provided: false,
      },
      []
    ) as unknown as MatchEach<input, output>;
  }

  return new MatchEachExpression<input, output>(
    { provided: true, value: value as input },
    []
  ) as unknown as MatchEach<input, output>;
}

/**
 * Builder for `matchEach`. Clauses are recorded and only executed when the
 * expression is finished with `.run`, `.exhaustive`, `.otherwise`, or a
 * compiled function.
 */
class MatchEachExpression<input, output> {
  constructor(
    private readonly inputSlot: InputSlot<input>,
    private readonly steps: readonly Step<input, output>[]
  ) {}

  with(...args: any[]): MatchEachExpression<input, output> {
    const handler: (selection: unknown, value: input) => output =
      args[args.length - 1];

    const patterns: Pattern<input>[] = [args[0]];
    let predicate: ((value: input) => unknown) | undefined = undefined;

    if (args.length === 3 && typeof args[1] === 'function') {
      predicate = args[1];
    } else if (args.length > 2) {
      patterns.push(...args.slice(1, args.length - 1));
    }

    const step: WithStep<input, output> = { type: 'with', patterns, handler };
    if (predicate) step.predicate = predicate;

    return new MatchEachExpression(this.inputSlot, [...this.steps, step]);
  }

  when(
    predicate: (value: input) => unknown,
    handler: (value: input, value2: input) => output
  ): MatchEachExpression<input, output> {
    const step: WhenStep<input, output> = { type: 'when', predicate, handler };
    return new MatchEachExpression(this.inputSlot, [...this.steps, step]);
  }

  tap(callback: (result: output) => void): MatchEachExpression<input, output> {
    const step: TapStep<output> = { type: 'tap', callback };
    return new MatchEachExpression(this.inputSlot, [...this.steps, step]);
  }

  otherwise(handler: (value: input) => output): output[] {
    const value = this.requireInput();
    const results = this.collect(value);
    if (results.length === 0) return [handler(value)];
    return results;
  }

  exhaustive(
    unexpectedValueHandler: (value: unknown) => output = defaultCatcher
  ): output[] {
    const value = this.requireInput();
    const results = this.collect(value);
    if (results.length === 0) return [unexpectedValueHandler(value)];
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
    return (input: input) => {
      const results = this.collect(input);
      if (results.length === 0) throw new NonExhaustiveError(input);
      return results;
    };
  }

  toExhaustiveFunction(): (input: input) => output[] {
    return this.toFunction();
  }

  toPartialFunction(): (input: input) => output[] | undefined {
    return (input: input) => {
      const results = this.collect(input);
      if (results.length === 0) return undefined;
      return results;
    };
  }

  private requireInput(): input {
    if (!this.inputSlot.provided) {
      throw new Error(noInputMessage);
    }
    return this.inputSlot.value;
  }

  private collect(value: input): output[] {
    const results: output[] = [];

    for (const step of this.steps) {
      if (step.type === 'tap') {
        for (const result of results) {
          step.callback(result);
        }
        continue;
      }

      if (step.type === 'when') {
        if (Boolean(step.predicate(value))) {
          results.push(step.handler(value, value));
        }
        continue;
      }

      const matched = evaluateWith(step, value);
      if (matched.matched) results.push(matched.value);
    }

    return results;
  }
}

function evaluateWith<input, output>(
  step: WithStep<input, output>,
  value: input
): { matched: true; value: output } | { matched: false; value?: undefined } {
  for (const pattern of step.patterns) {
    let hasSelections = false;
    const selected: Record<string, unknown> = {};
    const select = (key: string, selectedValue: unknown) => {
      hasSelections = true;
      selected[key] = selectedValue;
    };

    if (!matchPattern(pattern, value, select)) continue;

    if (step.predicate && !Boolean(step.predicate(value))) {
      return { matched: false };
    }

    const selections = hasSelections
      ? symbols.anonymousSelectKey in selected
        ? selected[symbols.anonymousSelectKey]
        : selected
      : value;

    return { matched: true, value: step.handler(selections, value) };
  }

  return { matched: false };
}

function defaultCatcher(input: unknown): never {
  throw new NonExhaustiveError(input);
}

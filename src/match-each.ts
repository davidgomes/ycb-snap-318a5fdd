import { Pattern } from './types/Pattern';
import { MatchEach } from './types/MatchEach';
import * as symbols from './internals/symbols';
import { matchPattern } from './internals/helpers';
import { NonExhaustiveError } from './errors';

type ClauseResult<output> =
  | { matched: true; value: output }
  | { matched: false; value: undefined };

type Step<input, output> =
  | { kind: 'clause'; evaluate: (input: input) => ClauseResult<output> }
  | { kind: 'tap'; callback: (result: output) => void };

const unmatched: ClauseResult<never> = { matched: false, value: undefined };

/**
 * `matchEach` creates a **pattern matching expression** that evaluates
 * every clause, and returns the results of all matching handlers
 * in declaration order.
 *
 * Call it without a value (using explicit type parameters) to build
 * a reusable function with `.toFunction()`, `.toExhaustiveFunction()`
 * or `.toPartialFunction()`.
 *
 * @example
 *  declare let input: number;
 *
 *  return matchEach(input)
 *    .with(P.number.positive(), () => 'positive')
 *    .with(P.number.int(), () => 'integer')
 *    .otherwise(() => 'other');
 *
 */
export function matchEach<input, output = symbols.unset>(): MatchEach<
  input,
  output
>;
export function matchEach<const input, output = symbols.unset>(
  value: input
): MatchEach<input, output>;
export function matchEach(...args: [] | [unknown]): unknown {
  return new MatchEachExpression(args[0], []);
}

/**
 * The types of this class aren't public, the public type definition
 * can be found in src/types/MatchEach.ts.
 */
class MatchEachExpression<input, output> {
  constructor(
    private input: input,
    private steps: readonly Step<input, output>[]
  ) {}

  private add(step: Step<input, output>): MatchEachExpression<input, output> {
    return new MatchEachExpression(this.input, [...this.steps, step]);
  }

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

    return this.add({
      kind: 'clause',
      evaluate: (input) => {
        let hasSelections = false;
        const selected: Record<string, unknown> = {};
        const select = (key: string, value: unknown) => {
          hasSelections = true;
          selected[key] = value;
        };

        const matched =
          patterns.some((pattern) => matchPattern(pattern, input, select)) &&
          (predicate ? Boolean(predicate(input)) : true);

        if (!matched) return unmatched;

        const selections = hasSelections
          ? symbols.anonymousSelectKey in selected
            ? selected[symbols.anonymousSelectKey]
            : selected
          : input;

        return { matched: true, value: handler(selections, input) };
      },
    });
  }

  when(
    predicate: (value: input) => unknown,
    handler: (selection: input, value: input) => output
  ): MatchEachExpression<input, output> {
    return this.add({
      kind: 'clause',
      evaluate: (input) =>
        predicate(input)
          ? { matched: true, value: handler(input, input) }
          : unmatched,
    });
  }

  tap(callback: (result: output) => void): MatchEachExpression<input, output> {
    return this.add({ kind: 'tap', callback });
  }

  private evaluate(input: input): output[] {
    const results: output[] = [];
    for (const step of this.steps) {
      if (step.kind === 'tap') {
        results.forEach((result) => step.callback(result));
      } else {
        const result = step.evaluate(input);
        if (result.matched) results.push(result.value);
      }
    }
    return results;
  }

  otherwise(handler: (value: input) => output): output[] {
    const results = this.evaluate(this.input);
    return results.length ? results : [handler(this.input)];
  }

  exhaustive(unexpectedValueHandler?: (value: input) => output): output[] {
    const results = this.evaluate(this.input);
    if (results.length) return results;
    if (unexpectedValueHandler) return [unexpectedValueHandler(this.input)];
    throw new NonExhaustiveError(this.input);
  }

  run(): output[] {
    return this.exhaustive();
  }

  returnType() {
    return this;
  }

  narrow() {
    return this;
  }

  toFunction(): (input: input) => output[] {
    return (input) => {
      const results = this.evaluate(input);
      if (!results.length) throw new NonExhaustiveError(input);
      return results;
    };
  }

  toExhaustiveFunction(): (input: input) => output[] {
    return this.toFunction();
  }

  toPartialFunction(): (input: input) => output[] | undefined {
    return (input) => {
      const results = this.evaluate(input);
      return results.length ? results : undefined;
    };
  }
}

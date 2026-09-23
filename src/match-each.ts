import { Pattern } from './types/Pattern';
import { MatchEach } from './types/MatchEach';
import * as symbols from './internals/symbols';
import { matchPattern } from './internals/helpers';
import { NonExhaustiveError } from './errors';

type Clause<input, output> = (value: input, results: output[]) => void;

/**
 * `matchEach` creates a pattern matching expression which, unlike `match`,
 * doesn't stop at the first matching pattern: it evaluates **every** clause
 * and returns the results of all matching handlers, in declaration order.
 *  * Use `.with(pattern, handler)` to pattern match on the input.
 *  * Use `.exhaustive()`, `.otherwise(() => defaultValue)` or `.run()` to end the expression and get the results.
 *
 * @example
 *  declare let input: number;
 *
 *  return matchEach(input)
 *    .with(P.number.positive(), () => 'positive')
 *    .with(P.number.int(), () => 'integer')
 *    .otherwise(() => 'other');
 *  // input = 2  => ['positive', 'integer']
 *  // input = -1 => ['integer']
 */
export function matchEach<const input, output = symbols.unset>(
  value: input
): MatchEach<input, output>;
/**
 * `matchEach<Input, Output>()` creates a pattern matching expression which
 * isn't bound to a value. Use `.toFunction()`, `.toExhaustiveFunction()`
 * or `.toPartialFunction()` to compile it into a reusable function.
 *
 * @example
 *  const classify = matchEach<number, string>()
 *    .with(P.number.positive(), () => 'positive')
 *    .with(P.number.int(), () => 'integer')
 *    .toPartialFunction();
 *
 *  classify(2);   // => ['positive', 'integer']
 *  classify(-.5); // => undefined
 */
export function matchEach<input, output = symbols.unset>(): MatchEach<
  input,
  output
>;
export function matchEach(value?: unknown): MatchEach<unknown, unknown> {
  return new MatchEachExpression(value, []) as any;
}

/**
 * This class represents a matchEach expression. Clauses are only
 * evaluated when calling `.exhaustive`, `.otherwise`, `.run`
 * or a function compiled with `.toFunction`, `.toExhaustiveFunction`
 * or `.toPartialFunction`.
 *
 * The types of this class aren't public, the public type definition
 * can be found in src/types/MatchEach.ts.
 */
class MatchEachExpression<input, output> {
  constructor(
    private input: input,
    private clauses: readonly Clause<input, output>[]
  ) {}

  with(...args: any[]): MatchEachExpression<input, output> {
    const handler: (selection: unknown, value: input) => output =
      args[args.length - 1];

    const patterns: Pattern<input>[] = [args[0]];
    let predicate: ((value: input) => unknown) | undefined = undefined;

    if (args.length === 3 && typeof args[1] === 'function') {
      // case with guard as second argument
      predicate = args[1];
    } else if (args.length > 2) {
      // case with several patterns
      patterns.push(...args.slice(1, args.length - 1));
    }

    return this.addClause((value, results) => {
      let hasSelections = false;
      const selected: Record<string, unknown> = {};
      const select = (key: string, selection: unknown) => {
        hasSelections = true;
        selected[key] = selection;
      };

      const matched =
        patterns.some((pattern) => matchPattern(pattern, value, select)) &&
        (predicate ? Boolean(predicate(value)) : true);

      if (!matched) return;

      const selections = hasSelections
        ? symbols.anonymousSelectKey in selected
          ? selected[symbols.anonymousSelectKey]
          : selected
        : value;

      results.push(handler(selections, value));
    });
  }

  when(
    predicate: (value: input) => unknown,
    handler: (selection: input, value: input) => output
  ): MatchEachExpression<input, output> {
    return this.addClause((value, results) => {
      if (predicate(value)) results.push(handler(value, value));
    });
  }

  tap(callback: (result: output) => void): MatchEachExpression<input, output> {
    return this.addClause((_value, results) => {
      results.forEach((result) => callback(result));
    });
  }

  otherwise(handler: (value: input) => output): output[] {
    const results = this.evaluate(this.input);
    return results.length ? results : [handler(this.input)];
  }

  exhaustive(unexpectedValueHandler = defaultCatcher): output[] {
    const results = this.evaluate(this.input);
    return results.length ? results : [unexpectedValueHandler(this.input)];
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
      return results.length ? results : defaultCatcher(input);
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

  private addClause(
    clause: Clause<input, output>
  ): MatchEachExpression<input, output> {
    return new MatchEachExpression(this.input, [...this.clauses, clause]);
  }

  private evaluate(value: input): output[] {
    const results: output[] = [];
    for (const clause of this.clauses) clause(value, results);
    return results;
  }
}

function defaultCatcher(input: unknown): never {
  throw new NonExhaustiveError(input);
}

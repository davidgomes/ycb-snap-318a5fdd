import { Pattern } from './types/Pattern';
import { MatchEach } from './types/MatchEach';
import * as symbols from './internals/symbols';
import { matchPattern } from './internals/helpers';
import { NonExhaustiveError } from './errors';

type Entry =
  | {
      kind: 'clause';
      run: (input: any) => { matched: boolean; value?: unknown };
    }
  | { kind: 'tap'; callback: (result: any) => void };

const noValue = Symbol('ts-pattern/matchEach/noValue');

/**
 * `matchEach` creates a pattern matching expression that evaluates **every**
 * clause and collects the results of all matching handlers into an array,
 * in declaration order.
 *
 * Call it without argument (with explicit type parameters) to build a
 * reusable function with `.toFunction()`, `.toExhaustiveFunction()` or
 * `.toPartialFunction()`.
 *
 * @example
 *  matchEach(2)
 *    .with(P.number, () => 'number')
 *    .with(2, () => 'two')
 *    .run(); // ['number', 'two']
 */
export function matchEach<const input, output = symbols.unset>(
  value: input
): MatchEach<input, output>;
export function matchEach<input, output = symbols.unset>(): MatchEach<
  input,
  output
>;
export function matchEach(...args: any[]): any {
  return new MatchEachExpression(args.length ? args[0] : noValue, []);
}

function evaluate(entries: Entry[], input: unknown): unknown[] {
  const results: unknown[] = [];
  for (const entry of entries) {
    if (entry.kind === 'tap') {
      for (const result of results) entry.callback(result);
    } else {
      const state = entry.run(input);
      if (state.matched) results.push(state.value);
    }
  }
  return results;
}

class MatchEachExpression {
  constructor(private input: unknown, private entries: Entry[]) {}

  private add(entry: Entry) {
    return new MatchEachExpression(this.input, [...this.entries, entry]);
  }

  private results(): unknown[] {
    return evaluate(this.entries, this.input);
  }

  with(...args: any[]) {
    const handler: (selection: unknown, value: unknown) => unknown =
      args[args.length - 1];

    const patterns: Pattern<unknown>[] = [args[0]];
    let predicate: ((value: unknown) => unknown) | undefined = undefined;

    if (args.length === 3 && typeof args[1] === 'function') {
      predicate = args[1];
    } else if (args.length > 2) {
      patterns.push(...args.slice(1, args.length - 1));
    }

    return this.add({
      kind: 'clause',
      run: (input) => {
        let hasSelections = false;
        const selected: Record<string, unknown> = {};
        const select = (key: string, value: unknown) => {
          hasSelections = true;
          selected[key] = value;
        };

        const matched =
          patterns.some((pattern) => matchPattern(pattern, input, select)) &&
          (predicate ? Boolean(predicate(input)) : true);

        if (!matched) return { matched: false };

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
    predicate: (value: unknown) => unknown,
    handler: (selection: unknown, value: unknown) => unknown
  ) {
    return this.add({
      kind: 'clause',
      run: (input) =>
        Boolean(predicate(input))
          ? { matched: true, value: handler(input, input) }
          : { matched: false },
    });
  }

  tap(callback: (result: unknown) => void) {
    return this.add({ kind: 'tap', callback });
  }

  otherwise(handler: (value: unknown) => unknown): unknown[] {
    const results = this.results();
    return results.length ? results : [handler(this.input)];
  }

  exhaustive(unexpectedValueHandler = defaultCatcher): unknown[] {
    const results = this.results();
    return results.length ? results : [unexpectedValueHandler(this.input)];
  }

  run(): unknown[] {
    return this.exhaustive();
  }

  toFunction() {
    const entries = this.entries;
    return (input: unknown) => {
      const results = evaluate(entries, input);
      if (!results.length) throw new NonExhaustiveError(input);
      return results;
    };
  }

  toExhaustiveFunction() {
    return this.toFunction();
  }

  toPartialFunction() {
    const entries = this.entries;
    return (input: unknown) => {
      const results = evaluate(entries, input);
      return results.length ? results : undefined;
    };
  }

  returnType() {
    return this;
  }

  narrow() {
    return this;
  }
}

function defaultCatcher(input: unknown): never {
  throw new NonExhaustiveError(input);
}

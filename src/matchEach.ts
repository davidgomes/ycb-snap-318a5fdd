import { Pattern } from './types/Pattern';
import { MatchEach } from './types/MatchEach';
import * as symbols from './internals/symbols';
import { matchPattern } from './internals/helpers';
import { NonExhaustiveError } from './errors';

type WithEntry<input, output> = {
  kind: 'with';
  patterns: Pattern<input>[];
  predicate?: (value: input) => unknown;
  handler: (selections: unknown, value: input) => output;
};

type WhenEntry<input, output> = {
  kind: 'when';
  predicate: (value: input) => unknown;
  handler: (value: input) => output;
};

type TapEntry<output> = {
  kind: 'tap';
  callback: (result: output) => void;
};

type Entry<input, output> =
  | WithEntry<input, output>
  | WhenEntry<input, output>
  | TapEntry<output>;

/**
 * `matchEach` creates a **pattern matching expression** that evaluates all
 * registered patterns and collects every matching handler's result into an
 * array, returned in the order clauses were declared.
 *
 * @example
 *  declare let input: number;
 *
 *  return matchEach(input)
 *    .with(P.number.positive(), () => 'positive')
 *    .with(P.number.negative(), () => 'negative')
 *    .otherwise(() => ['zero']);
 *
 */
export function matchEach<const input, output = symbols.unset>(
  value?: input
): MatchEach<input, output> {
  return new MatchEachExpression(value) as any;
}

class MatchEachExpression<input, output> {
  constructor(
    private readonly value: input | undefined,
    private readonly entries: Entry<input, output>[] = []
  ) {}

  private clone(entries: Entry<input, output>[]): MatchEachExpression<input, output> {
    return new MatchEachExpression(this.value, entries);
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

    return this.clone([
      ...this.entries,
      { kind: 'with', patterns, predicate, handler },
    ]);
  }

  when(
    predicate: (value: input) => unknown,
    handler: (value: input) => output
  ): MatchEachExpression<input, output> {
    return this.clone([
      ...this.entries,
      { kind: 'when', predicate, handler },
    ]);
  }

  tap(callback: (result: output) => void): MatchEachExpression<input, output> {
    return this.clone([...this.entries, { kind: 'tap', callback }]);
  }

  private evaluate(value: input): output[] {
    const results: output[] = [];

    for (const entry of this.entries) {
      if (entry.kind === 'tap') {
        for (const result of results) {
          entry.callback(result);
        }
        continue;
      }

      if (entry.kind === 'when') {
        if (Boolean(entry.predicate(value))) {
          results.push(entry.handler(value));
        }
        continue;
      }

      let hasSelections = false;
      let selected: Record<string, unknown> = {};
      const select = (key: string, selectionValue: unknown) => {
        hasSelections = true;
        selected[key] = selectionValue;
      };

      const matched =
        entry.patterns.some((pattern) =>
          matchPattern(pattern, value, select)
        ) && (entry.predicate ? Boolean(entry.predicate(value)) : true);

      if (matched) {
        const selections = hasSelections
          ? symbols.anonymousSelectKey in selected
            ? selected[symbols.anonymousSelectKey]
            : selected
          : value;

        results.push(entry.handler(selections, value));
      }
    }

    return results;
  }

  otherwise(handler: (value: input) => output): output[] {
    const input = this.getInput();
    const results = this.evaluate(input);
    if (results.length === 0) {
      return [handler(input)];
    }
    return results;
  }

  exhaustive(unexpectedValueHandler = defaultCatcher): output[] {
    const input = this.getInput();
    const results = this.evaluate(input);
    if (results.length === 0) {
      return [unexpectedValueHandler(input as unknown as input)];
    }
    return results;
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

  toFunction(): (value: input) => output[] {
    return (value: input) => {
      const results = this.evaluate(value);
      if (results.length === 0) {
        throw new NonExhaustiveError(value);
      }
      return results;
    };
  }

  toExhaustiveFunction(): (value: input) => output[] {
    return this.toFunction();
  }

  toPartialFunction(): (value: input) => output[] | undefined {
    return (value: input) => {
      const results = this.evaluate(value);
      return results.length === 0 ? undefined : results;
    };
  }

  private getInput(): input {
    if (this.value === undefined) {
      throw new Error(
        'matchEach: cannot run without an input value. Use toFunction(), toExhaustiveFunction(), or toPartialFunction() for reusable matchers.'
      );
    }
    return this.value;
  }
}

function defaultCatcher(input: unknown): never {
  throw new NonExhaustiveError(input);
}

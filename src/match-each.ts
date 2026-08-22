import { Pattern } from './types/Pattern';
import { MatchEach } from './types/MatchEach';
import * as symbols from './internals/symbols';
import { matchPattern } from './internals/helpers';
import { NonExhaustiveError } from './errors';

type Clause<input, output> = {
  patterns: Pattern<input>[];
  predicate?: (value: input) => unknown;
  handler: (selection: unknown, value: input) => output;
};

type Entry<input, output> =
  | { kind: 'clause'; clause: Clause<input, output> }
  | { kind: 'tap'; callback: (result: output) => void };

export function matchEach<const input, output = symbols.unset>(
  value?: input
): MatchEach<input, output> {
  return new MatchEachExpression(arguments.length > 0, value, []) as any;
}

class MatchEachExpression<input, output> {
  constructor(
    private readonly hasValue: boolean,
    private readonly value: input | undefined,
    private readonly entries: Entry<input, output>[]
  ) {}

  with(...args: any[]): MatchEachExpression<input, output> {
    const handler = args[args.length - 1];
    const patterns: Pattern<input>[] = [args[0]];
    let predicate: ((value: input) => unknown) | undefined;

    if (args.length === 3 && typeof args[1] === 'function') {
      predicate = args[1];
    } else if (args.length > 2) {
      patterns.push(...args.slice(1, args.length - 1));
    }

    return this.append({
      kind: 'clause',
      clause: { patterns, predicate, handler },
    });
  }

  when(
    predicate: (value: input) => unknown,
    handler: (value: input, originalValue: input) => output
  ): MatchEachExpression<input, output> {
    return this.append({
      kind: 'clause',
      clause: {
        patterns: [],
        predicate,
        handler: (selection, value) => handler(selection as input, value),
      },
    });
  }

  otherwise(handler: (value: input) => output): output[] {
    const results = this.evaluate();
    return results.length === 0 ? [handler(this.value as input)] : results;
  }

  exhaustive(fallback: (value: unknown) => output = defaultCatcher): output[] {
    const results = this.evaluate();
    return results.length === 0 ? [fallback(this.value)] : results;
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

  tap(callback: (result: output) => void): MatchEachExpression<input, output> {
    return this.append({ kind: 'tap', callback });
  }

  toFunction(): (input: input) => output[] {
    return (input) => {
      const results = this.evaluate(input);
      if (results.length === 0) throw new NonExhaustiveError(input);
      return results;
    };
  }

  toExhaustiveFunction(): (input: input) => output[] {
    return (input) => {
      const results = this.evaluate(input);
      if (results.length === 0) throw new NonExhaustiveError(input);
      return results;
    };
  }

  toPartialFunction(): (input: input) => output[] | undefined {
    return (input) => {
      const results = this.evaluate(input);
      return results.length === 0 ? undefined : results;
    };
  }

  private append(
    entry: Entry<input, output>
  ): MatchEachExpression<input, output> {
    return new MatchEachExpression(this.hasValue, this.value, [
      ...this.entries,
      entry,
    ]);
  }

  private evaluate(value = this.value as input): output[] {
    const results: output[] = [];

    for (const entry of this.entries) {
      if (entry.kind === 'tap') {
        results.forEach((result) => entry.callback(result));
        continue;
      }

      let selection: unknown = value;
      let matched = false;
      for (const pattern of entry.clause.patterns) {
        let hasSelections = false;
        const selected: Record<string, unknown> = {};
        const select = (key: string, selectedValue: unknown) => {
          hasSelections = true;
          selected[key] = selectedValue;
        };
        if (matchPattern(pattern, value, select)) {
          selection = hasSelections
            ? symbols.anonymousSelectKey in selected
              ? selected[symbols.anonymousSelectKey]
              : selected
            : value;
          matched = true;
          break;
        }
      }

      if (entry.clause.patterns.length === 0) matched = true;
      if (
        matched &&
        (!entry.clause.predicate || entry.clause.predicate(value))
      ) {
        results.push(entry.clause.handler(selection, value));
      }
    }

    return results;
  }
}

function defaultCatcher(input: unknown): never {
  throw new NonExhaustiveError(input);
}

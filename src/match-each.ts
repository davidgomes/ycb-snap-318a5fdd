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

type Tap<input, output> = (value: input, result: output) => void;

export function matchEach<const input, output = symbols.unset>(
  value: input
): MatchEach<input, output>;
export function matchEach<output = symbols.unset>(): MatchEach<unknown, output>;
export function matchEach(value?: unknown): MatchEach<unknown, unknown> {
  return new MatchEachExpression(value, []);
}

class MatchEachExpression<input, output> {
  constructor(
    private readonly input: input,
    private readonly clauses: Clause<input, output>[],
    private readonly taps: Tap<input, output>[] = []
  ) {}

  with(...args: any[]): MatchEachExpression<input, output> {
    const handler = args[args.length - 1];
    const patterns = [args[0], ...args.slice(1, args.length - 1)].filter(
      (pattern) => typeof pattern !== 'function'
    );
    const predicate =
      args.length === 3 && typeof args[1] === 'function' ? args[1] : undefined;
    return new MatchEachExpression(this.input, [
      ...this.clauses,
      { patterns, predicate, handler },
    ], this.taps);
  }

  when(predicate: (value: input) => unknown, handler: (value: input) => output) {
    return new MatchEachExpression(this.input, [
      ...this.clauses,
      { patterns: [], predicate, handler: (selection) => handler(selection as input) },
    ], this.taps);
  }

  tap(callback: Tap<input, output>) {
    return new MatchEachExpression(this.input, this.clauses, [
      ...this.taps,
      callback,
    ]);
  }

  otherwise(handler: (value: input) => output): output[] {
    const results = this.evaluate();
    return results.length ? results : [handler(this.input)];
  }

  exhaustive(fallback = defaultCatcher): output[] {
    const results = this.evaluate();
    return results.length ? results : [fallback(this.input)];
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

  toFunction() {
    return (input: input) => this.evaluate(input);
  }

  toExhaustiveFunction() {
    return (input: input) => this.exhaustiveFor(input);
  }

  toPartialFunction() {
    return (input: input) => {
      const results = this.evaluate(input);
      return results.length ? results : undefined;
    };
  }

  private exhaustiveFor(input: input): output[] {
    const results = this.evaluate(input);
    if (!results.length) throw new NonExhaustiveError(input);
    return results;
  }

  private evaluate(input = this.input): output[] {
    const results: output[] = [];
    for (const clause of this.clauses) {
      let selections: Record<string, unknown> = {};
      let hasSelections = false;
      const select = (key: string, selected: unknown) => {
        hasSelections = true;
        selections[key] = selected;
      };
      const matched =
        (clause.patterns.length === 0 ||
          clause.patterns.some((pattern) => matchPattern(pattern, input, select))) &&
        (!clause.predicate || Boolean(clause.predicate(input)));
      if (matched) {
        const selection = hasSelections
          ? symbols.anonymousSelectKey in selections
            ? selections[symbols.anonymousSelectKey]
            : selections
          : input;
        const result = clause.handler(selection, input);
        results.push(result);
        for (const tap of this.taps) tap(input, result);
      }
    }
    return results;
  }
}

function defaultCatcher(input: unknown): never {
  throw new NonExhaustiveError(input);
}

import { Pattern } from './types/Pattern';
import { MatchEach } from './types/MatchEach';
import * as symbols from './internals/symbols';
import { matchPattern } from './internals/helpers';
import { NonExhaustiveError } from './errors';

type InputState<input> =
  | { readonly hasValue: true; readonly value: input }
  | { readonly hasValue: false };

type WithClause<input, output> = {
  readonly type: 'with';
  readonly patterns: readonly unknown[];
  readonly predicate?: (value: input) => unknown;
  readonly handler: (selections: unknown, value: input) => output;
};

type WhenClause<input, output> = {
  readonly type: 'when';
  readonly predicate: (value: input) => unknown;
  readonly handler: (value: input, input: input) => output;
};

type TapClause<output> = {
  readonly type: 'tap';
  readonly callback: (result: output) => unknown;
};

type Clause<input, output> =
  | WithClause<input, output>
  | WhenClause<input, output>
  | TapClause<output>;

/**
 * `matchEach` creates a pattern matching expression that evaluates **every**
 * registered clause and collects each matching handler's result into an array,
 * in declaration order.
 *
 *  * Use `.with(pattern, handler)` or `.when(predicate, handler)` to register clauses.
 *  * Use `.tap(callback)` to observe results collected so far.
 *  * Use `.run()`, `.exhaustive()`, or `.otherwise(handler)` to evaluate an input value.
 *  * Use `.toFunction()`, `.toExhaustiveFunction()`, or `.toPartialFunction()` to compile a reusable matcher.
 *
 * @example
 *  declare let input: 'a' | 'b';
 *
 *  return matchEach(input)
 *    .with('a', () => 'A')
 *    .with(P.union('a', 'b'), () => 'ab')
 *    .exhaustive();
 *  // input 'a' => ['A', 'ab']
 *  // input 'b' => ['ab']
 */
export function matchEach<const input, output = symbols.unset>(
  value: input
): MatchEach<input, output>;
export function matchEach<const input, output = symbols.unset>(): MatchEach<
  input,
  output
>;
export function matchEach<const input, output = symbols.unset>(
  value?: input
): MatchEach<input, output> {
  const inputState: InputState<input> =
    arguments.length === 0
      ? { hasValue: false }
      : { hasValue: true, value: value as input };

  return new MatchEachExpression(inputState, []) as unknown as MatchEach<
    input,
    output
  >;
}

export type { MatchEach };

class MatchEachExpression<input, output> {
  constructor(
    private inputState: InputState<input>,
    private clauses: readonly Clause<input, output>[]
  ) {}

  private continueWith(
    clause: Clause<input, output>
  ): MatchEachExpression<input, output> {
    return new MatchEachExpression(this.inputState, [...this.clauses, clause]);
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

    return this.continueWith({
      type: 'with',
      patterns,
      predicate,
      handler,
    });
  }

  when(
    predicate: (value: input) => unknown,
    handler: (selection: input, value: input) => output
  ): MatchEachExpression<input, output> {
    return this.continueWith({
      type: 'when',
      predicate,
      handler,
    });
  }

  tap(
    callback: (result: output) => unknown
  ): MatchEachExpression<input, output> {
    return this.continueWith({
      type: 'tap',
      callback,
    });
  }

  otherwise(handler: (value: input) => output): output[] {
    const results = this.evaluate(this.requireValue());
    if (results.length === 0) return [handler(this.requireValue())];
    return results;
  }

  exhaustive(
    unexpectedValueHandler: (value: unknown) => output = defaultCatcher
  ): output[] {
    const value = this.requireValue();
    const results = this.evaluate(value);
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

  toFunction(): (value: input) => output[] {
    return (value: input) => {
      const results = this.evaluate(value);
      if (results.length === 0) throw new NonExhaustiveError(value);
      return results;
    };
  }

  toExhaustiveFunction(): (value: input) => output[] {
    return this.toFunction();
  }

  toPartialFunction(): (value: input) => output[] | undefined {
    return (value: input) => {
      const results = this.evaluate(value);
      if (results.length === 0) return undefined;
      return results;
    };
  }

  private requireValue(): input {
    if (!this.inputState.hasValue) {
      throw new Error(
        'matchEach() was called without an input value. Pass a value, or compile the expression with .toFunction(), .toExhaustiveFunction(), or .toPartialFunction().'
      );
    }
    return this.inputState.value;
  }

  private evaluate(value: input): output[] {
    const results: output[] = [];

    for (const clause of this.clauses) {
      if (clause.type === 'tap') {
        for (const result of results) {
          clause.callback(result);
        }
        continue;
      }

      if (clause.type === 'when') {
        if (clause.predicate(value)) {
          results.push(clause.handler(value, value));
        }
        continue;
      }

      const matched = matchWithClause(clause, value);
      if (matched.matched) results.push(matched.result);
    }

    return results;
  }
}

function matchWithClause<input, output>(
  clause: WithClause<input, output>,
  value: input
): { matched: true; result: output } | { matched: false } {
  for (const pattern of clause.patterns) {
    // Fresh selection state per pattern so a non-matching alternative
    // cannot leak named selections into the handler.
    let hasSelections = false;
    const selected: Record<string, unknown> = {};
    const select = (key: string, selectedValue: unknown) => {
      hasSelections = true;
      selected[key] = selectedValue;
    };

    if (!matchPattern(pattern, value, select)) continue;

    if (clause.predicate && !clause.predicate(value)) return { matched: false };

    const selections = hasSelections
      ? symbols.anonymousSelectKey in selected
        ? selected[symbols.anonymousSelectKey]
        : selected
      : value;

    return { matched: true, result: clause.handler(selections, value) };
  }

  return { matched: false };
}

function defaultCatcher(input: unknown): never {
  throw new NonExhaustiveError(input);
}

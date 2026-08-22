import { matchEach, P, NonExhaustiveError } from '../src';
import { Equal, Expect } from '../src/types/helpers';

describe('matchEach', () => {
  it('collects every matching clause in declaration order', () => {
    expect(
      matchEach(4)
        .with(P.number, (value) => `number:${value}`)
        .with(
          P.when((value) => value % 2 === 0),
          () => 'even'
        )
        .with(4, () => 'four')
        .run()
    ).toEqual(['number:4', 'even', 'four']);
  });

  it('supports guards, fallback behavior, and independent selections', () => {
    const matcher = matchEach<{ kind: 'a' | 'b'; value: number }>()
      .with({ kind: 'a', value: P.select('value') }, ({ value }) => value)
      .with({ kind: 'a', value: P.select('value') }, ({ value }) => value * 2)
      .when(
        (value) => value.kind === 'b',
        () => 0
      );
    const fn = matcher.toPartialFunction();

    expect(fn({ kind: 'a', value: 3 })).toEqual([3, 6]);
    expect(fn({ kind: 'b', value: 3 })).toEqual([0]);
    expect(fn({ kind: 'a', value: 3 })).toEqual([3, 6]);
  });

  it('runs taps for results collected so far', () => {
    const tapped: string[] = [];
    const result = matchEach('ok')
      .with(P.string, () => 'first')
      .tap((value) => tapped.push(value))
      .with('ok', () => 'second')
      .tap((value) => tapped.push(value))
      .run();

    expect(result).toEqual(['first', 'second']);
    expect(tapped).toEqual(['first', 'first', 'second']);
  });

  it('throws for an unmatched run and supports exhaustive fallback', () => {
    expect(() =>
      matchEach<'x' | 'y'>('x')
        .with('y', () => 1)
        .run()
    ).toThrow(NonExhaustiveError);
    expect(
      matchEach<'x' | 'y'>('z' as 'x' | 'y')
        .with('x', () => 1)
        .with('y', () => 1)
        .exhaustive((value) => value)
    ).toEqual(['z']);
  });

  it('supports exhaustive and reusable matcher types', () => {
    const matcher = matchEach<'a' | 'b'>()
      .with('a', () => 1)
      .with('b', () => 2);
    const fn = matcher.toExhaustiveFunction();
    const result = fn('a');

    type t = Expect<Equal<typeof result, number[]>>;
    expect(result).toEqual([1]);
  });

  it('runs taps in compiled matchers', () => {
    const tapped: number[] = [];
    const fn = matchEach<number>()
      .with(P.number, (value) => value)
      .tap((value) => tapped.push(value))
      .toFunction();

    expect(fn(2)).toEqual([2]);
    expect(fn(3)).toEqual([3]);
    expect(tapped).toEqual([2, 3]);
  });
});

import { matchEach, P, NonExhaustiveError } from '../src';

describe('matchEach', () => {
  it('collects all matching results in order', () => {
    const res = matchEach<number | string>(2)
      .with(P.number, () => 'number')
      .with(2, () => 'two')
      .with(P.string, () => 'string')
      .run();
    expect(res).toEqual(['number', 'two']);
  });

  it('throws when nothing matches', () => {
    expect(() =>
      matchEach(3 as number)
        .with(2, () => 'two')
        .run()
    ).toThrow(NonExhaustiveError);
  });

  it('otherwise and exhaustive fallback', () => {
    expect(
      matchEach(3 as number)
        .with(2, () => 'two')
        .otherwise(() => 'x')
    ).toEqual(['x']);
    const input = 'a' as 'a' | 'b';
    expect(
      matchEach(input)
        .with('b', () => 1)
        .with('a', () => 2)
        .exhaustive()
    ).toEqual([2]);
    // @ts-expect-error
    expect(() => matchEach(input).with('b', () => 1).exhaustive()).toThrow();
  });

  it('isolates selections and supports taps and compiled functions', () => {
    const seen: string[] = [];
    const fn = matchEach<{ a: string; b: string }>()
      .with({ a: P.select('x') }, ({ x }) => x)
      .tap((r) => seen.push(r))
      .with({ b: P.select('y') }, (sel) => {
        expect(Object.keys(sel)).toEqual(['y']);
        return sel.y;
      })
      .tap((r) => seen.push(r))
      .toFunction();
    expect(fn({ a: '1', b: '2' })).toEqual(['1', '2']);
    expect(fn({ a: '3', b: '4' })).toEqual(['3', '4']);
    expect(seen).toEqual(['1', '1', '2', '3', '3', '4']);

    const partial = matchEach<number>()
      .with(1, () => 'one')
      .toPartialFunction();
    expect(partial(2)).toBeUndefined();

    const exh = matchEach<'a' | 'b'>()
      .with('a', () => 1)
      .with('b', () => 2)
      .toExhaustiveFunction();
    expect(exh('b')).toEqual([2]);
  });
});

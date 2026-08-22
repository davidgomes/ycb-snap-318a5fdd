import type { Match } from './Match';

export type MatchEach<i, o> = Omit<Match<i, o>, 'otherwise' | 'exhaustive' | 'run' | 'narrow' | 'returnType'> & {
  otherwise<c>(handler: (value: i) => c): o extends never ? c[] : (o | c)[];
  exhaustive<c = never>(fallback?: (value: i) => c): (o | c)[];
  run(): o[];
  returnType: <output>() => MatchEach<i, output>;
  narrow(): MatchEach<i, o>;
  tap(callback: (value: i, result: o) => void): MatchEach<i, o>;
  toFunction(): (input: i) => o[];
  toExhaustiveFunction(): (input: i) => o[];
  toPartialFunction(): (input: i) => o[] | undefined;
};

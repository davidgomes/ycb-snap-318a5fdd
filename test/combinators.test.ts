import { describe, expect, test, vi } from 'vitest';
import * as maybe from 'true-myth/maybe';
import * as result from 'true-myth/result';
import * as task from 'true-myth/task';
import * as toolbelt from 'true-myth/toolbelt';
import { Task } from 'true-myth/task';

describe('iterators', () => {
  test('Maybe and Result', () => {
    expect([...maybe.just(1)]).toEqual([1]);
    expect([...maybe.nothing()]).toEqual([]);
    expect([...result.ok(1)]).toEqual([1]);
    expect([...result.err('e')]).toEqual([]);
  });

  test('Task yields exactly one Result', async () => {
    const out = [];
    for await (const r of task.reject('no')) out.push(r);
    for await (const r of task.resolve(1)) out.push(r);
    expect(out).toEqual([result.err('no'), result.ok(1)]);
  });
});

describe('maybe', () => {
  test('sequence stops at first Nothing', () => {
    let pulled = 0;
    function* gen() {
      pulled++;
      yield maybe.just(1);
      pulled++;
      yield maybe.nothing<number>();
      pulled++;
      yield maybe.just(3);
    }
    expect(maybe.sequence(gen())).toEqual(maybe.nothing());
    expect(pulled).toBe(2);
    expect(maybe.sequence([maybe.just(1), maybe.just(2)])).toEqual(maybe.just([1, 2]));
  });

  test('traverse, zip, zipWith, compact, filterMap, firstJust', () => {
    const half = (n: number) => (n % 2 === 0 ? maybe.just(n / 2) : maybe.nothing<number>());
    expect(maybe.traverse([2, 4], half)).toEqual(maybe.just([1, 2]));
    expect(maybe.traverse(half)([2, 3])).toEqual(maybe.nothing());
    expect(maybe.zip(maybe.just(1), maybe.just('a'))).toEqual(maybe.just([1, 'a']));
    expect(maybe.zipWith(maybe.just(1), maybe.just(2), (a, b) => a + b)).toEqual(maybe.just(3));
    expect(maybe.zip(maybe.nothing<number>(), maybe.just(1))).toEqual(maybe.nothing());
    expect(maybe.zipWith(maybe.just(1), maybe.nothing<number>(), (a, b) => a + b)).toEqual(
      maybe.nothing()
    );
    expect(maybe.compact([maybe.just(1), maybe.nothing<number>()])).toEqual([1]);
    expect(maybe.filterMap([1, 2, 4], half)).toEqual([1, 2]);
    expect(maybe.filterMap(half)([3])).toEqual([]);
    expect(maybe.firstJust([maybe.nothing<number>(), maybe.just(2)])).toEqual(maybe.just(2));
    expect(maybe.firstJust([])).toEqual(maybe.nothing());
  });
});

describe('result', () => {
  test('sequence, traverse, zip, zipWith, partition', () => {
    expect(result.sequence([result.ok(1), result.err('a'), result.err('b')])).toEqual(
      result.err('a')
    );
    const check = (n: number) => (n > 0 ? result.ok(n) : result.err(`bad ${n}`));
    expect(result.traverse([1, 2], check)).toEqual(result.ok([1, 2]));
    expect(result.traverse(check)([1, -1])).toEqual(result.err('bad -1'));
    expect(result.zip(result.ok(1), result.ok(2))).toEqual(result.ok([1, 2]));
    expect(result.zipWith(result.ok(1), result.err('x'), (a, b: number) => a + b)).toEqual(
      result.err('x')
    );
    expect(result.zip(result.err('a'), result.ok(1))).toEqual(result.err('a'));
    expect(result.zip(result.ok(1), result.err('b'))).toEqual(result.err('b'));
    expect(result.partition([result.ok(1), result.err('e'), result.ok(2)])).toEqual([
      [1, 2],
      ['e'],
    ]);
  });
});

describe('task', () => {
  test('sequence, traverse, zip, zipWith', async () => {
    expect(await task.sequence([task.resolve(1), task.resolve(2)])).toEqual(result.ok([1, 2]));
    expect(await task.traverse([1, 2], (n) => task.resolve(n * 2))).toEqual(result.ok([2, 4]));
    expect(await task.zip(task.resolve(1), task.reject('x'))).toEqual(result.err('x'));
    expect(await task.zipWith(task.resolve(1), task.resolve(2), (a, b) => a + b)).toEqual(
      result.ok(3)
    );
  });

  test('traverseSerial stops on first rejection', async () => {
    const seen: number[] = [];
    const run = task.traverseSerial((n: number) => {
      seen.push(n);
      return n === 2 ? task.reject<number, string>('boom') : task.resolve<number, string>(n);
    });
    expect(await run([1, 2, 3])).toEqual(result.err('boom'));
    expect(seen).toEqual([1, 2]);
  });

  test('tap and tapRejected', async () => {
    const fn = vi.fn();
    expect(await task.tap(task.resolve(1), fn)).toEqual(result.ok(1));
    expect(await task.tap(fn)(task.resolve(2))).toEqual(result.ok(2));
    expect(await task.tapRejected(task.reject('e'), fn)).toEqual(result.err('e'));
    expect(fn.mock.calls).toEqual([[1], [2], ['e']]);
  });

  test('retryN', async () => {
    let calls = 0;
    const flaky = () => (++calls < 3 ? Task.reject<number, number>(calls) : Task.resolve(calls));
    expect(await task.retryN(2, flaky)).toEqual(result.ok(3));
    calls = 0;
    expect(await task.retryN(1, flaky)).toEqual(result.err(2));
  });
});

describe('toolbelt', () => {
  test('MaybeAsResult helpers', () => {
    expect(toolbelt.sequenceMaybeAsResult('none', [maybe.just(1)])).toEqual(result.ok([1]));
    expect(toolbelt.sequenceMaybeAsResult('none')([maybe.nothing()])).toEqual(result.err('none'));
    expect(toolbelt.traverseMaybeAsResult('none', [1], (n) => maybe.just(n))).toEqual(
      result.ok([1])
    );
    expect(toolbelt.traverseMaybeAsResult('none')([1], () => maybe.nothing())).toEqual(
      result.err('none')
    );
    expect(toolbelt.zipMaybeAsResult('none', maybe.just(1), maybe.just(2))).toEqual(
      result.ok([1, 2])
    );
    expect(toolbelt.zipMaybeAsResult('none')(maybe.just(1), maybe.nothing())).toEqual(
      result.err('none')
    );
  });
});

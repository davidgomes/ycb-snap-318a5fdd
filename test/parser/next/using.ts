import * as t from 'node:assert/strict';
import { describe, it } from 'vitest';
import { parseSource } from '../../../src/parser';
import { fail, pass } from '../../test-utils';

describe('Next - using and await using', () => {
  const next = { next: true };

  pass('Next - using (pass)', [
    { code: '{ using x = null; }', options: next },
    { code: '{ using x = null }', options: next },
    { code: '{ using a = 1, b = 2; }', options: next },
    { code: '{ using of = 1; }', options: next },
    { code: '{ using using = null; }', options: next },
    { code: 'function f() { using x = null; }', options: next },
    { code: 'async function f() { await using x = null; }', options: next },
    { code: 'async function f() { using x = null; }', options: next },
    { code: 'const f = async () => { await using x = null; };', options: next },
    { code: 'using x = null;', options: { ...next, sourceType: 'module' } },
    { code: 'await using x = null;', options: { ...next, sourceType: 'module' } },
    { code: '{ await using x = null; }', options: { ...next, sourceType: 'module' } },
    { code: 'export using x = null;', options: { ...next, sourceType: 'module' } },
    { code: 'export await using x = null;', options: { ...next, sourceType: 'module' } },
    { code: 'for (using x of y);', options: next },
    { code: 'for (using x = 1;;);', options: next },
    { code: 'for (using of y);', options: next },
    { code: 'for (using of of y);', options: next },
    { code: 'async function f() { for (await using x of y); }', options: next },
    { code: 'async function f() { for await (using x of y); }', options: next },
    { code: 'async function f() { for await (await using x of y); }', options: next },
    { code: 'for (await using x of y);', options: { ...next, sourceType: 'module' } },
    { code: 'for await (using x of y);', options: { ...next, sourceType: 'module' } },
    { code: 'class C { static { using x = null; } }', options: next },
    { code: 'function f() { using\nfoo = 1 }', options: next },
    { code: 'var using = 1;', options: next },
    { code: 'function using() {}', options: next },
    { code: 'var using = 1;' },
  ]);

  fail('Next - using (fail)', [
    { code: 'using foo = null', options: next },
    { code: 'using foo = null', options: { ...next, sourceType: 'commonjs' } },
    { code: 'await using foo = null', options: next },
    { code: 'function f() { await using x = null; }', options: next },
    { code: 'function f() { await using x = null; }', options: { ...next, sourceType: 'module' } },
    { code: '{ using x; }', options: next },
    { code: '{ using { x } = y; }', options: next },
    { code: '{ using [x] = y; }', options: next },
    { code: 'for (using x in y);', options: next },
    { code: 'for (using x = 1 in y);', options: next },
    { code: 'for (using { x } of y);', options: next },
    { code: 'for (await using x of y);', options: next },
    { code: 'class C { static { await using x = null; } }', options: next },
    { code: '{ using x = null }' },
  ]);

  it('reports the async-context error for await using at script top-level', () => {
    t.throws(
      () => parseSource('await using foo = null', { next: true }),
      (error: Error) =>
        error.message.includes('only allowed inside async') &&
        !error.message.includes('not allowed in the global scope'),
    );
  });
});

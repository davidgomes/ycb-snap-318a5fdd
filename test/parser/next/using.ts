import { describe } from 'vitest';
import { fail, pass } from '../../test-utils';

const next = { next: true };
const module = { next: true, sourceType: 'module' } as const;

describe('Next - Explicit resource management', () => {
  fail('Next - Explicit resource management (fail)', [
    { code: 'using x = a', options: next },
    { code: 'await using x = a', options: next },
    { code: 'function f() { await using x = a }', options: next },
    { code: 'class C { static { await using x = a } }', options: module },
    { code: '{ using x }', options: next },
    { code: '{ using x = a, y }', options: next },
    { code: '{ using {a} = b }', options: next },
    { code: 'async function f() { await using {a} = b }', options: next },
    { code: 'for (using x in y);', options: next },
    { code: 'async function f() { for (await using x in y); }', options: next },
    { code: 'for (using x = a of y);', options: next },
    { code: 'for (using x, y of z);', options: next },
    { code: '{ using x = a }', options: { sourceType: 'module' } },
  ]);

  pass('Next - Explicit resource management (pass)', [
    { code: '{ using x = a, y = b }', options: next },
    { code: 'using x = a', options: module },
    { code: 'await using x = a', options: module },
    { code: 'async function f() { await using x = a; }', options: next },
    { code: 'function f() { using x = a; }', options: next },
    { code: '{ using\nx = a }', options: next },
    { code: 'using(x)', options: next },
    { code: 'using in x', options: next },
    { code: '{ using [a] = b }', options: next },
    { code: 'for (using x of y);', options: next },
    { code: 'for (using of of y);', options: next },
    { code: 'for (using of y);', options: next },
    { code: 'for (using x = a;;);', options: next },
    { code: 'for (await using x of y);', options: module },
    { code: 'async function f() { for await (await using x of y); }', options: next },
    { code: 'async function f() { await\nusing; }', options: next },
  ]);
});

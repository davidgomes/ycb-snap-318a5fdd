import { describe } from 'vitest';
import { fail, pass } from '../../test-utils';

describe('Next - Explicit resource management', () => {
  pass('Using declarations (pass)', [
    { code: '{ using x = y; }', options: { next: true } },
    { code: '{ using x = y, z = w; }', options: { next: true } },
    { code: 'function f() { using x = y; }', options: { next: true } },
    { code: 'switch (a) { case 1: using x = y; }', options: { next: true } },
    { code: 'class C { static { using x = y; } }', options: { next: true } },
    { code: 'using x = y;', options: { next: true, sourceType: 'module' } },
    { code: 'using x = y;', options: { next: true, sourceType: 'module', ranges: true, loc: true } },
    { code: '{ using x = y; using z = x; }', options: { next: true, lexical: true } },
    { code: 'for (using x of y);', options: { next: true } },
    { code: 'for (using x = y; ;);', options: { next: true } },
    { code: 'async function f() { for await (using x of y); }', options: { next: true } },
    { code: 'async function f() { await using x = y; }', options: { next: true } },
    { code: 'async function f() { await using x = y, z = w; }', options: { next: true } },
    { code: 'async () => { await using x = y; }', options: { next: true } },
    { code: 'await using x = y;', options: { next: true, sourceType: 'module' } },
    { code: '{ await using x = y; }', options: { next: true, sourceType: 'module' } },
    { code: 'for (await using x of y);', options: { next: true, sourceType: 'module' } },
    { code: 'async function f() { for (await using x of y); }', options: { next: true } },
    { code: 'async function f() { for await (await using x of y); }', options: { next: true } },
    { code: 'async function f() { for (await using of of y); }', options: { next: true } },
    // `using` as an identifier
    { code: 'using\nx = y;', options: { next: true } },
    { code: 'using[x] = y;', options: { next: true } },
    { code: 'using.x = y;', options: { next: true } },
    { code: 'using(x);', options: { next: true } },
    { code: 'using => x;', options: { next: true } },
    { code: 'using: x;', options: { next: true } },
    { code: 'using instanceof X;', options: { next: true } },
    { code: '{ using in x; }', options: { next: true } },
    { code: 'var using = x;', options: { next: true } },
    { code: String.raw`var us\u0069ng = x;`, options: { next: true } },
    { code: '{ using\nx = y; }', options: { next: true } },
    { code: 'for (using of x);', options: { next: true } },
    { code: 'for (using[x] of y);', options: { next: true } },
    { code: 'for (using in x);', options: { next: true } },
    { code: 'for (using; ;);', options: { next: true } },
    { code: 'async function f() { await using; }', options: { next: true } },
    { code: 'async function f() { await using[x]; }', options: { next: true } },
    { code: 'async function f() { await using\nx; }', options: { next: true } },
    { code: 'await\nusing\nx', options: { next: true } },
  ]);

  fail('Using declarations (fail)', [
    // Script global scope
    { code: 'using x = y;', options: { next: true } },
    { code: 'using x = y;', options: { next: true, sourceType: 'commonjs' } },
    // `await using` outside async/module
    { code: 'await using x = y;', options: { next: true } },
    { code: 'function f() { await using x = y; }', options: { next: true } },
    { code: 'function f() { await using x = y; }', options: { next: true, sourceType: 'module' } },
    { code: '{ await using x = y; }', options: { next: true } },
    { code: 'for (await using x of y);', options: { next: true } },
    { code: 'function f() { for (await using x of y); }', options: { next: true, sourceType: 'module' } },
    { code: 'class C { static { await using x = y; } }', options: { next: true } },
    // Missing initializer
    { code: '{ using x; }', options: { next: true } },
    { code: '{ using x = y, z; }', options: { next: true } },
    { code: 'async function f() { await using x; }', options: { next: true } },
    { code: 'for (using x; ;);', options: { next: true } },
    // For-in
    { code: 'for (using x in y);', options: { next: true } },
    { code: 'async function f() { for (await using x in y); }', options: { next: true } },
    // Destructuring
    { code: '{ using {x} = y; }', options: { next: true } },
    { code: '{ using x = y, [z] = w; }', options: { next: true } },
    { code: 'async function f() { await using {x} = y; }', options: { next: true } },
    { code: 'for (using {x} of y);', options: { next: true } },
    // Other
    { code: '{ using let = x; }', options: { next: true } },
    { code: '{ using x = y; using x = z; }', options: { next: true, lexical: true } },
    { code: 'if (a) using x = y;', options: { next: true } },
    { code: 'label: using x = y;', options: { next: true } },
    { code: 'for (using x = y of z);', options: { next: true } },
    { code: 'for (using of of y);', options: { next: true } },
    { code: 'async function f() { await\nusing x = y; }', options: { next: true } },
    { code: String.raw`{ us\u0069ng x = y; }`, options: { next: true } },
    { code: '{ using x = y; }', options: {} },
  ]);
});

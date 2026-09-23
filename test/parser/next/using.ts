import { describe } from 'vitest';
import { fail, pass } from '../../test-utils';

describe('Next - Using declarations', () => {
  pass('Using declarations (pass)', [
    { code: '{ using x = null; }', options: { next: true } },
    { code: '{ using x = a, y = b; }', options: { next: true, ranges: true, loc: true } },
    { code: 'function f() { using x = resource(); }', options: { next: true } },
    { code: '() => { using x = null; }', options: { next: true } },
    { code: 'class C { static { using x = null; } }', options: { next: true } },
    { code: 'switch (0) { case 0: { using x = null; } }', options: { next: true } },
    { code: '{ using as = a, async = b, of = c, get = d, static = e; }', options: { next: true } },
    { code: 'using x = null;', options: { next: true, sourceType: 'module' } },
    { code: '{ using x = null; } { using x = null; }', options: { next: true, lexical: true } },

    { code: 'async function f() { await using x = null; }', options: { next: true } },
    { code: 'async () => { await using x = a, y = b; }', options: { next: true, ranges: true, loc: true } },
    { code: 'await using x = null;', options: { next: true, sourceType: 'module' } },
    { code: '{ await using x = null; }', options: { next: true, sourceType: 'module' } },

    { code: 'for (using x of y);', options: { next: true } },
    { code: 'for (using x = null;;) break;', options: { next: true } },
    { code: 'for (using of = null;;) break;', options: { next: true } },
    { code: 'async function f() { for (await using x of y); }', options: { next: true } },
    { code: 'async function f() { for await (using x of y); }', options: { next: true } },
    { code: 'async function f() { for await (await using x of y); }', options: { next: true } },
    { code: 'async function f() { for (await using of of y); }', options: { next: true } },
    { code: 'for (await using x of y);', options: { next: true, sourceType: 'module' } },

    // `using` as an identifier
    { code: 'using;', options: { next: true } },
    { code: 'using = 1; using.x; using(x); using[x] = 1; using`x`;', options: { next: true } },
    { code: 'using in x; using instanceof X;', options: { next: true } },
    { code: '{ using\nx = null; }', options: { next: true } },
    { code: 'using => using;', options: { next: true } },
    { code: 'using: for (;;) break using;', options: { next: true } },
    { code: 'for (using of y);', options: { next: true } },
    { code: 'for (using of of [0, 1, 2]);', options: { next: true } },
    { code: 'for (using in y);', options: { next: true } },
    { code: 'for (using[x] of y);', options: { next: true } },
    { code: 'for (using;;);', options: { next: true } },
    { code: 'async function f() { await using; await using[x]; await using\nx = null; }', options: { next: true } },
    { code: 'function f() { await; using x = null; }', options: { next: true } },
    { code: String.raw`\u0075sing = null;`, options: { next: true } },
    { code: 'using = null; ({ using }); class C { using() {} }', options: {} },
  ]);

  fail('Using declarations (fail)', [
    { code: 'using x = null;', options: { next: true } },
    { code: 'await using x = null;', options: { next: true } },
    { code: 'function f() { await using x = null; }', options: { next: true } },
    { code: '{ await using x = null; }', options: { next: true } },
    { code: 'for (await using x of y);', options: { next: true } },
    { code: 'class C { static { await using x = null; } }', options: { next: true, sourceType: 'module' } },
    { code: 'async function f() { class C { static { await using x = null; } } }', options: { next: true } },

    { code: '{ using x; }', options: { next: true } },
    { code: '{ using x = null, y; }', options: { next: true } },
    { code: 'async function f() { await using x; }', options: { next: true } },
    { code: 'for (using x;;);', options: { next: true } },

    { code: 'for (using x in y);', options: { next: true } },
    { code: 'async function f() { for (await using x in y); }', options: { next: true } },

    { code: '{ using {a} = b; }', options: { next: true } },
    { code: '{ using x = null, [a] = b; }', options: { next: true } },
    { code: 'async function f() { await using {a} = b; }', options: { next: true } },
    { code: 'for (using {a} of b);', options: { next: true } },

    { code: 'switch (0) { case 0: using x = null; }', options: { next: true } },
    { code: 'switch (0) { default: using x = null; }', options: { next: true } },
    { code: 'async function f() { switch (0) { case 0: await using x = null; } }', options: { next: true } },

    { code: '{ using let = null; }', options: { next: true } },
    { code: '{ using x = null; let x; }', options: { next: true, lexical: true } },
    { code: 'if (x) using y = null;', options: { next: true } },
    { code: '{ label: using y = null; }', options: { next: true } },
    { code: 'async function f() { await\nusing x = null; }', options: { next: true } },
    { code: 'for (using\nx of y);', options: { next: true } },
    { code: 'for (using x = null of y);', options: { next: true } },
    { code: String.raw`{ \u0075sing x = null; }`, options: { next: true } },
    { code: 'export using x = null;', options: { next: true, sourceType: 'module' } },

    { code: '{ using x = null; }', options: {} },
    { code: 'async function f() { await using x = null; }', options: {} },
  ]);
});

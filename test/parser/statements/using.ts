import { describe, expect, it } from 'vitest';
import { parseSource } from '../../../src/parser';
import { fail, pass } from '../../test-utils';

const next = { next: true } as const;

describe('Statements - Using', () => {
  pass('Using declarations (pass)', [
    { code: '{ using x = null; }', options: next },
    { code: '{ using x = 1, y = 2; }', options: next },
    { code: '{ using /*c*/ x = 1; }', options: next },
    { code: 'using\nx = 1;', options: next },
    { code: 'using /*\n*/ x = 1;', options: next },
    { code: '{ using = 1; }', options: next },
    { code: '{ using[a] = b; }', options: next },
    { code: '{ using instanceof x; }', options: next },
    { code: 'using: 1;', options: next },
    { code: '{ let using = 1; }', options: next },
    { code: 'foo.using;', options: next },
    { code: 'class C { using = 1; }', options: next },
    { code: 'function f(){ using x = 1; }', options: next },
    { code: 'switch (x) { case 1: { using y = x; } }', options: next },
    { code: 'for (using x of y);', options: next },
    { code: 'for (using x = null;;);', options: next },
    { code: 'for (using of = 1;;);', options: next },
    // `[lookahead ≠ using of]` — `using` is the LHS, `of[0, 1, 2]` is the RHS.
    { code: 'for (using of of [0, 1, 2]);', options: next },
    { code: 'async function f(){ await using x = 1; }', options: next },
    { code: 'async function f(){ for (await using x of y); }', options: next },
    { code: 'async function f(){ for await (using x of y); }', options: next },
    { code: 'async function f(){ for (await using of of []); }', options: next },
    { code: 'using x = null;', options: { ...next, sourceType: 'module' } },
    { code: 'await using y = null;', options: { ...next, sourceType: 'module' } },
    { code: 'export using x = null;', options: { ...next, sourceType: 'module' } },
    { code: 'export await using y = null;', options: { ...next, sourceType: 'module' } },
    { code: '{ await using x = null; }', options: { ...next, sourceType: 'module' } },
    { code: 'for (await using x of y);', options: { ...next, sourceType: 'module' } },
    { code: 'class C { static { using x = 1; await using y = 2; } }', options: { ...next, sourceType: 'module' } },
  ]);

  fail('Using declarations (fail)', [
    { code: 'using x = null', options: next },
    { code: 'await using x = null', options: next },
    { code: '{ using x; }', options: next },
    { code: '{ using { a } = b; }', options: next },
    { code: '{ await using x = 1; }', options: next },
    { code: 'function f(){ await using x = 1; }', options: next },
    { code: 'for (using x in y);', options: next },
    { code: 'for (using of of []);', options: next },
    { code: 'for (using x = 1 in y);', options: next },
    { code: 'for (await using x of y);', options: next },
    { code: 'switch (x) { case 1: using y = x; }', options: next },
    { code: 'async function f(){ switch (x) { default: await using y = x; } }', options: next },
    { code: '{ using x = 1; }', options: { sourceType: 'script' } },
    { code: String.raw`{ us\u0069ng x = 1; }`, options: next },
    { code: '{ using a = 1; let a = 2; }', options: { ...next, lexical: true } },
  ]);

  it('reports the async-context error for await using at script top level', () => {
    expect(() => parseSource('await using x = null', next)).toThrow(/only allowed inside async/);
    expect(() => parseSource('await using x = null', next)).not.toThrow(/global scope/);
  });
});

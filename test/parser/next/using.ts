import { describe, it } from 'vitest';
import { parseSource } from '../../../src/parser';
import { fail, pass } from '../../test-utils';

describe('Next - using', () => {
  const next = { next: true };

  pass('using declarations (pass)', [
    { code: '{ using x = null; }', options: next },
    { code: '{ using x = null, y = 1; }', options: next },
    { code: 'function f(){ using x = null; }', options: next },
    { code: 'using x = null;', options: { ...next, sourceType: 'module' } },
    { code: 'await using x = null;', options: { ...next, sourceType: 'module' } },
    { code: 'async function f(){ await using x = null; }', options: next },
    { code: 'for (using x of y);', options: next },
    { code: 'for (using x = 1;;);', options: next },
    { code: 'async function f(){ for (await using x of y); }', options: next },
    { code: 'async function f(){ for await (using x of y); }', options: next },
    { code: 'async function f(){ for await (await using x of y); }', options: next },
    { code: 'for (using x of y);', options: { ...next, sourceType: 'module' } },
    { code: 'for (await using x of y);', options: { ...next, sourceType: 'module' } },
    { code: 'switch (0) { default: using x = null; }', options: next },
    // Line break: `using` is an identifier, not a declaration.
    { code: 'using\nfoo;', options: next },
  ]);

  fail('using declarations (fail)', [
    { code: 'using foo = null', options: next },
    { code: 'await using foo = null', options: next },
    { code: 'function f(){ await using foo = null }', options: next },
    { code: '{ using foo; }', options: next },
    { code: '{ using { x } = null; }', options: next },
    { code: '{ using [x] = null; }', options: next },
    { code: 'for (using x in y);', options: next },
    { code: 'async function f(){ for (await using x in y); }', options: next },
    { code: 'for (await using x of y);', options: next },
    { code: 'class C { static { await using x = null; } }', options: next },
  ]);

  it('does not treat using as a keyword when next is disabled', () => {
    try {
      parseSource('using foo = null');
      throw new Error('expected a syntax error');
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      if (!message.includes('Unexpected token')) throw error;
    }
  });
});

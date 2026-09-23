import { describe, expect, it } from 'vitest';
import { type Options } from '../../../src/options';
import { parseSource } from '../../../src/parser';
import { fail, pass } from '../../test-utils';

const script: Options = { next: true };
const module: Options = { next: true, sourceType: 'module' };

describe('Statements - Using', () => {
  pass('Statements - Using (pass)', [
    { code: '{ using x = f(); }', options: script },
    { code: '{ using x = f(), y = g(); }', options: script },
    { code: '{ using x = null }', options: { ...script, ranges: true, loc: true } },
    { code: 'function f() { using x = g(); }', options: script },
    { code: '() => { using x = g(); }', options: script },
    { code: 'class C { static { using x = f(); } }', options: script },
    { code: 'using x = f();', options: module },
    { code: 'await using x = f();', options: module },
    { code: '{ await using x = f(); }', options: module },
    { code: 'async function f() { await using x = g(), y = h(); }', options: script },
    { code: 'async () => { await using x = g(); }', options: script },
    { code: 'for (using x of y);', options: script },
    { code: 'for (using x = f();;);', options: script },
    { code: 'for (using of = f();;);', options: script },
    { code: 'async function f() { for (await using x of y); }', options: script },
    { code: 'async function f() { for await (using x of y); }', options: script },
    { code: 'async function f() { for await (await using x of y); }', options: script },
    { code: 'for (await using x of y);', options: module },
    { code: 'for await (await using x of y);', options: module },
    { code: 'async function f() { for (await using of of []); }', options: script },
    { code: 'switch (x) { case 1: { using y = f(); } }', options: script },
    { code: '{ using x = f() }', options: { ...script, lexical: true } },
    // `using` is still an identifier when not followed by a binding on the same line
    { code: '{ using\nx = f(); }', options: script },
    { code: '{ using /*\n*/ x = f(); }', options: script },
    { code: '{ using[x] = f(); }', options: script },
    { code: 'using.x = y', options: script },
    { code: 'using => 0', options: script },
    { code: 'var using; using in x; using instanceof y', options: script },
    { code: 'for (using of y);', options: script },
    { code: 'for (using of of [0]);', options: script },
    { code: 'for (using in y);', options: script },
    { code: 'async function f() { await using; await using[0]; await\nusing; }', options: script },
    { code: 'await using\nx', options: module },
    // without `next`, `using` is always an identifier
    { code: 'using\nx = y', options: {} },
  ]);

  fail('Statements - Using (fail)', [
    { code: 'using x = f();', options: script },
    { code: 'using x = f();', options: { ...script, sourceType: 'commonjs' } },
    { code: 'await using x = f();', options: script },
    { code: 'function f() { await using x = g(); }', options: script },
    { code: '() => { await using x = g(); }', options: module },
    { code: 'class C { static { await using x = f(); } }', options: module },
    { code: 'for (await using x of y);', options: script },
    { code: '{ using x; }', options: script },
    { code: '{ using x = f(), y; }', options: script },
    { code: 'await using x;', options: module },
    { code: 'for (using x;;);', options: script },
    { code: 'for (using x in y);', options: script },
    { code: 'for (using x = f() in y);', options: script },
    { code: 'for (await using x in y);', options: module },
    { code: '{ using {x} = f(); }', options: script },
    { code: '{ using x = f(), [y] = g(); }', options: script },
    { code: 'await using {x} = f();', options: module },
    { code: 'for (using {x} of y);', options: script },
    { code: 'switch (x) { case 1: using y = f(); }', options: script },
    { code: 'switch (x) { default: await using y = f(); }', options: module },
    { code: 'if (x) using y = f();', options: script },
    { code: 'label: using x = f();', options: script },
    { code: '{ using x = f(), x = g(); }', options: { ...script, lexical: true } },
    { code: '{ using let = f(); }', options: script },
    { code: String.raw`{ \u0075sing x = f(); }`, options: script },
    { code: '{ using x = f(); }', options: {} },
  ]);

  it('reports the expected error messages', () => {
    const expectError = (code: string, options: Options, message: string) =>
      expect(() => parseSource(code, options)).toThrow(message);

    expectError('using x = f();', script, 'not allowed in the global scope');
    expectError('await using x = f();', script, 'only allowed inside async');
    expectError('function f() { await using x = g(); }', script, 'only allowed inside async');
    expectError('{ using x; }', script, 'must have an initializer');
    expectError('await using x;', module, 'must have an initializer');
    expectError('for (using x in y);', script, 'not allowed in for-in');
    expectError('{ using {x} = f(); }', script, 'cannot have destructuring');
  });

  it('produces `VariableDeclaration` nodes with `using` kinds', () => {
    expect(parseSource('{ using x = f(); }', script).body[0]).toMatchObject({
      type: 'BlockStatement',
      body: [{ type: 'VariableDeclaration', kind: 'using' }],
    });
    expect(parseSource('await using x = f();', module).body[0]).toMatchObject({
      type: 'VariableDeclaration',
      kind: 'await using',
    });
    expect(parseSource('for (using x of y);', script).body[0]).toMatchObject({
      type: 'ForOfStatement',
      left: { type: 'VariableDeclaration', kind: 'using' },
    });
  });
});

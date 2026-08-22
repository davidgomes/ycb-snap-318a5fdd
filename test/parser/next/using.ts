import * as assert from 'node:assert/strict';
import { describe, it } from 'vitest';
import { parseSource } from '../../../src/parser';

describe('Next - using declarations', () => {
  it('parses using declarations', () => {
    const program = parseSource('function f() { using resource = acquire(); }', { next: true });
    const declaration = (program.body[0] as any).body.body[0];
    assert.equal(declaration.type, 'VariableDeclaration');
    assert.equal(declaration.kind, 'using');
  });

  it('parses await using declarations in async functions and modules', () => {
    assert.doesNotThrow(() => parseSource('async function f() { await using resource = acquire(); }', { next: true }));
    assert.doesNotThrow(() => parseSource('await using resource = acquire();', { next: true, sourceType: 'module' }));
  });

  it('parses using declarations in for-of heads', () => {
    assert.doesNotThrow(() => parseSource('for (using resource of resources) {}', { next: true }));
    assert.doesNotThrow(() =>
      parseSource('async function f() { for (await using resource of resources) {} }', { next: true }),
    );
    assert.doesNotThrow(() =>
      parseSource('async function f() { for await (using resource of resources) {} }', { next: true }),
    );
  });

  it('reports using declaration restrictions', () => {
    for (const code of [
      'using resource = acquire();',
      'await using resource = acquire();',
      'function f() { using resource; }',
      'function f() { using { resource } = acquire(); }',
      'for (using resource = acquire();;);',
      'for (using resource in resources);',
    ]) {
      assert.throws(() => parseSource(code, { next: true }));
    }

    assert.throws(() => parseSource('await using resource = acquire();', { next: true }), /only allowed inside async/);
    assert.throws(() => parseSource('function f() { using resource; }', { next: true }), /must have an initializer/);
    assert.throws(
      () => parseSource('function f() { using { resource } = acquire(); }', { next: true }),
      /cannot have destructuring/,
    );
    assert.throws(() => parseSource('for (using resource in resources);', { next: true }), /not allowed in for-in/);
  });

  it('treats using followed by a line break as an identifier', () => {
    const program = parseSource('function f() { using\nfoo = null; }', { next: true });
    const statements = (program.body[0] as any).body.body;
    assert.equal(statements[0].type, 'ExpressionStatement');
    assert.equal(statements[0].expression.name, 'using');
    assert.equal(statements[1].type, 'ExpressionStatement');
    assert.equal(statements[1].expression.type, 'AssignmentExpression');
    assert.equal(statements[1].expression.left.name, 'foo');
  });
});

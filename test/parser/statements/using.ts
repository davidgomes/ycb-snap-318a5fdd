import * as assert from 'node:assert/strict';
import { describe, it } from 'vitest';
import { parseSource } from '../../../src/parser';

const next = { next: true };

describe('Statements - using declarations', () => {
  it('emits using declaration kinds', () => {
    assert.deepEqual(parseSource('{ using resource = acquire(); }', next).body[0], {
      type: 'BlockStatement',
      body: [
        {
          type: 'VariableDeclaration',
          kind: 'using',
          declarations: [
            {
              type: 'VariableDeclarator',
              id: { type: 'Identifier', name: 'resource' },
              init: {
                type: 'CallExpression',
                callee: { type: 'Identifier', name: 'acquire' },
                arguments: [],
                optional: false,
              },
            },
          ],
        },
      ],
    });

    assert.equal(
      parseSource('async function f() { await using resource = acquire(); }', next).body[0].body!.body[0].kind,
      'await using',
    );
    assert.equal(parseSource('using resource = acquire();', { ...next, sourceType: 'module' }).body[0].kind, 'using');
    assert.equal(
      parseSource('await using resource = acquire();', { ...next, sourceType: 'module' }).body[0].kind,
      'await using',
    );
  });

  it('accepts using declarations in for-of heads', () => {
    assert.equal(parseSource('for (using resource of resources) {}', next).body[0].left.type, 'VariableDeclaration');
    assert.equal(parseSource('for (using resource of resources) {}', next).body[0].left.kind, 'using');
    assert.equal(
      parseSource('async function f() { for (await using resource of resources) {} }', next).body[0].body!.body[0].left
        .kind,
      'await using',
    );
    assert.equal(
      parseSource('async function f() { for await (using resource of resources) {} }', next).body[0].body!.body[0].left
        .kind,
      'using',
    );
  });

  it('requires the appropriate declaration context and shape', () => {
    assert.throws(() => parseSource('using resource = acquire();', next), /not allowed in the global scope/);
    assert.throws(() => parseSource('await using resource = acquire();', next), /only allowed inside async/);
    assert.throws(() => parseSource('{ using resource; }', next), /must have an initializer/);
    assert.throws(() => parseSource('{ using { resource } = acquire(); }', next), /cannot have destructuring/);
    assert.throws(() => parseSource('for (using resource in resources) {}', next), /not allowed in for-in/);
  });

  it('treats a line break after using as an identifier boundary', () => {
    assert.doesNotThrow(() => parseSource('{ using\nresource = acquire(); }', next));
  });
});

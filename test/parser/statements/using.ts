import * as assert from 'node:assert/strict';
import { describe, it } from 'vitest';
import type * as ESTree from '../../../src/estree';
import { parseSource } from '../../../src/parser';

const next = { next: true };

function getVariableDeclaration(statement: ESTree.Statement): ESTree.VariableDeclaration {
  if (statement.type !== 'VariableDeclaration') throw new Error('Expected a variable declaration');
  return statement;
}

function getForOfStatement(statement: ESTree.Statement): ESTree.ForOfStatement {
  if (statement.type !== 'ForOfStatement') throw new Error('Expected a for-of statement');
  return statement;
}

function getResourceKind(initializer: ESTree.ForInitializer): 'using' | 'await using' {
  if (initializer.type !== 'VariableDeclaration') throw new Error('Expected a resource declaration');
  return initializer.kind;
}

function getFunctionBody(statement: ESTree.Statement): ESTree.BlockStatement {
  if (statement.type !== 'FunctionDeclaration' || !statement.body) throw new Error('Expected a function declaration');
  return statement.body;
}

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
      getVariableDeclaration(
        getFunctionBody(parseSource('async function f() { await using resource = acquire(); }', next).body[0]).body[0],
      ).kind,
      'await using',
    );
    assert.equal(
      getVariableDeclaration(parseSource('using resource = acquire();', { ...next, sourceType: 'module' }).body[0])
        .kind,
      'using',
    );
    assert.equal(
      getVariableDeclaration(
        parseSource('await using resource = acquire();', { ...next, sourceType: 'module' }).body[0],
      ).kind,
      'await using',
    );
  });

  it('accepts using declarations in for-of heads', () => {
    const forOf = getForOfStatement(parseSource('for (using resource of resources) {}', next).body[0]);
    assert.equal(forOf.left.type, 'VariableDeclaration');
    assert.equal(getResourceKind(forOf.left), 'using');
    assert.equal(
      getResourceKind(
        getForOfStatement(
          getFunctionBody(
            parseSource('async function f() { for (await using resource of resources) {} }', next).body[0],
          ).body[0],
        ).left,
      ),
      'await using',
    );
    assert.equal(
      getResourceKind(
        getForOfStatement(
          getFunctionBody(
            parseSource('async function f() { for await (using resource of resources) {} }', next).body[0],
          ).body[0],
        ).left,
      ),
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

import { describe } from 'vitest';
import { fail, pass } from '../../test-utils';

describe('Next - Using declarations', () => {
  pass('Using declarations (pass)', [
    { code: '{ using resource = value; }', options: { next: true } },
    { code: 'async function f() { using resource = value; await using other = value; }', options: { next: true } },
    { code: 'using resource = value;', options: { sourceType: 'module', next: true } },
    { code: 'await using resource = value;', options: { sourceType: 'module', next: true } },
    { code: 'for (using resource of resources) {}', options: { next: true } },
    { code: 'async function f() { for (await using resource of resources) {} }', options: { next: true } },
    { code: 'async function f() { for await (using resource of resources) {} }', options: { next: true } },
    { code: 'using\nresource = value;', options: { next: true } },
  ]);

  fail('Using declarations (fail)', [
    { code: 'using resource = value;', options: { next: true } },
    { code: 'await using resource = value;', options: { next: true } },
    { code: 'function f() { await using resource = value; }', options: { next: true } },
    { code: '{ using resource; }', options: { next: true } },
    { code: '{ using { resource } = value; }', options: { next: true } },
    { code: 'for (using resource in resources) {}', options: { next: true } },
  ]);
});

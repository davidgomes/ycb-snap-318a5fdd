import { describe, expect, test } from 'vitest';
import { brand, readonly, transform } from '../../actions/index.ts';
import {
  array,
  intersect,
  literal,
  map,
  nullable,
  number,
  object,
  optional,
  record,
  set,
  string,
  union,
} from '../../schemas/index.ts';
import { parse } from '../parse/index.ts';
import { pipe } from '../pipe/index.ts';
import { safeParse } from '../safeParse/index.ts';
import { Recur, type RecurSchema } from './recur.ts';
import { recursive, type RecursiveSchema } from './recursive.ts';

describe('recursive', () => {
  test('should return schema object', () => {
    const wrapped = object({ child: Recur });
    expect(Recur).toStrictEqual({
      kind: 'schema',
      type: 'recur',
      reference: Recur.reference,
      expects: 'unknown',
      async: false,
      '~standard': {
        version: 1,
        vendor: 'valibot',
        validate: expect.any(Function),
      },
      '~run': expect.any(Function),
    } satisfies RecurSchema);
    expect(recursive(wrapped)).toStrictEqual({
      kind: 'schema',
      type: 'recursive',
      reference: recursive,
      expects: 'Object',
      async: false,
      wrapped,
      '~standard': {
        version: 1,
        vendor: 'valibot',
        validate: expect.any(Function),
      },
      '~run': expect.any(Function),
    } satisfies RecursiveSchema<typeof wrapped>);
  });

  test('should throw for unresolved Recur placeholder', () => {
    expect(() => Recur['~run']({ value: { child: null } }, {})).toThrowError(
      'Unresolved Recur placeholder. Wrap the schema with recursive or recursiveAsync.'
    );
    expect(() =>
      object({ child: Recur })['~run']({ value: { child: null } }, {})
    ).toThrowError(/Unresolved Recur placeholder/u);
  });

  describe('object', () => {
    const schema = recursive(
      object({
        value: string(),
        children: array(Recur),
      })
    );
    const input = {
      value: 'root',
      children: [
        { value: 'child', children: [] },
        {
          value: 'nested',
          children: [{ value: 'leaf', children: [] }],
        },
      ],
    };

    test('should parse recursive objects', () => {
      expect(parse(schema, input)).toStrictEqual(input);
      expect(safeParse(schema, input)).toStrictEqual({
        typed: true,
        success: true,
        output: input,
        issues: undefined,
      });
    });

    test('should return nested issues', () => {
      const dataset = schema['~run'](
        { value: { value: 123, children: [{ value: 'ok', children: [] }] } },
        { lang: 'en' }
      );
      expect(dataset.issues?.[0]).toMatchObject({
        type: 'string',
        path: [{ type: 'object', origin: 'value', key: 'value' }],
        lang: 'en',
      });
    });

    test('should keep config when recurring', () => {
      const dataset = schema['~run'](
        {
          value: {
            value: 'ok',
            children: [{ value: 123, children: [] }],
          },
        },
        { lang: 'de' }
      );
      expect(dataset.issues?.[0]).toMatchObject({
        type: 'string',
        lang: 'de',
        path: [
          { type: 'object', origin: 'value', key: 'children' },
          { type: 'array', origin: 'value', key: 0 },
          { type: 'object', origin: 'value', key: 'value' },
        ],
      });
    });
  });

  test('should parse array value recursion', () => {
    const schema = recursive(array(optional(Recur)));
    expect(parse(schema, [[], [[]]])).toStrictEqual([[], [[]]]);
    expect(parse(schema, [undefined, [[]]])).toStrictEqual([undefined, [[]]]);
  });

  test('should parse record value recursion', () => {
    const schema = recursive(
      record(string(), object({ id: string(), child: optional(Recur) }))
    );
    const input = {
      root: { id: 'a', child: { b: { id: 'b' } } },
    };
    expect(parse(schema, input)).toStrictEqual(input);
  });

  test('should parse map value recursion', () => {
    const schema = recursive(
      map(string(), object({ id: number(), next: optional(Recur) }))
    );
    const input = new Map([
      ['a', { id: 1, next: new Map([['b', { id: 2 }]]) }],
    ]);
    expect(parse(schema, input)).toStrictEqual(input);
  });

  test('should parse set value recursion', () => {
    const schema = recursive(
      set(object({ id: string(), nested: optional(Recur) }))
    );
    const leaf = { id: 'leaf' };
    const input = new Set([{ id: 'root', nested: new Set([leaf]) }]);
    expect(parse(schema, input)).toStrictEqual(input);
  });

  test('should compose through pipe and preserve transforms', () => {
    const schema = recursive(
      pipe(
        object({
          name: pipe(string(), brand('name')),
          nodes: array(Recur),
        }),
        transform((input) => ({ ...input, count: input.nodes.length })),
        readonly()
      )
    );
    expect(
      parse(schema, {
        name: 'root',
        nodes: [{ name: 'child', nodes: [] }],
      })
    ).toStrictEqual({
      name: 'root',
      count: 1,
      nodes: [{ name: 'child', count: 0, nodes: [] }],
    });
  });

  test('should compose through intersect', () => {
    const schema = recursive(
      intersect([
        object({ name: string() }),
        object({ child: nullable(Recur) }),
      ])
    );
    const input = { name: 'root', child: { name: 'child', child: null } };
    expect(parse(schema, input)).toStrictEqual(input);
  });

  test('should compose through union', () => {
    const schema = recursive(
      union([
        object({ type: literal('leaf'), value: string() }),
        object({ type: literal('branch'), children: array(Recur) }),
      ])
    );
    const input = {
      type: 'branch',
      children: [
        { type: 'leaf', value: 'a' },
        { type: 'branch', children: [] },
      ],
    };
    expect(parse(schema, input)).toStrictEqual(input);
  });

  test('should resolve nested recursive schemas independently', () => {
    const leaf = recursive(
      object({
        kind: literal('leaf'),
        next: optional(Recur),
      })
    );
    const tree = recursive(
      object({
        kind: literal('tree'),
        leaf,
        child: optional(Recur),
      })
    );
    const input = {
      kind: 'tree',
      leaf: { kind: 'leaf', next: { kind: 'leaf' } },
      child: {
        kind: 'tree',
        leaf: { kind: 'leaf' },
      },
    };
    expect(parse(tree, input)).toStrictEqual(input);
  });

  test('should wrap a schema that does not use Recur', () => {
    const schema = recursive(object({ name: string() }));
    expect(parse(schema, { name: 'valibot' })).toStrictEqual({
      name: 'valibot',
    });
  });
});

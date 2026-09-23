import { describe, expect, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  array,
  intersect,
  lazy,
  literal,
  map,
  null_,
  number,
  object,
  optional,
  record,
  set,
  string,
  tuple,
  union,
  variant,
} from '../../schemas/index.ts';
import type {
  BaseIssue,
  BaseSchema,
  Config,
  OutputDataset,
} from '../../types/index.ts';
import { config } from '../config/index.ts';
import { message } from '../message/index.ts';
import { pipe } from '../pipe/index.ts';
import { Recur, recursive } from './recursive.ts';

/**
 * Returns the path keys of the issues of a dataset.
 *
 * @param dataset The output dataset.
 *
 * @returns The path keys of each issue.
 */
function getIssueKeys(
  dataset: OutputDataset<unknown, BaseIssue<unknown>>
): unknown[][] | undefined {
  return dataset.issues?.map((issue) =>
    (issue.path ?? []).map((item) => item.key)
  );
}

describe('Recur', () => {
  test('should return schema object', () => {
    expect(Recur).toStrictEqual({
      kind: 'schema',
      type: 'recur',
      reference: recursive,
      expects: 'unknown',
      async: false,
      '~standard': {
        version: 1,
        vendor: 'valibot',
        validate: expect.any(Function),
      },
      '~run': expect.any(Function),
    });
  });

  describe('should throw error if placeholder is not resolved', () => {
    const errorMessage =
      'Recur must be resolved with recursive or recursiveAsync before parsing.';

    test('for placeholder itself', () => {
      expect(() => Recur['~run']({ value: {} }, {})).toThrowError(errorMessage);
    });

    test('for nested placeholder', () => {
      expect(() =>
        object({ children: array(Recur) })['~run'](
          { value: { children: [{}] } },
          {}
        )
      ).toThrowError(errorMessage);
    });

    test('for Standard Schema validation', () => {
      expect(() => Recur['~standard'].validate({})).toThrowError(errorMessage);
    });
  });
});

describe('recursive', () => {
  describe('should return schema object', () => {
    test('for object schema', () => {
      const schema = object({ key: string(), children: array(Recur) });
      expect(recursive(schema)).toStrictEqual({
        ...schema,
        '~standard': {
          version: 1,
          vendor: 'valibot',
          validate: expect.any(Function),
        },
        '~run': expect.any(Function),
      });
    });

    test('for schema with pipe', () => {
      const schema = pipe(
        object({ key: string(), children: array(Recur) }),
        transform((input) => input.children)
      );
      expect(recursive(schema)).toStrictEqual({
        ...schema,
        '~standard': {
          version: 1,
          vendor: 'valibot',
          validate: expect.any(Function),
        },
        '~run': expect.any(Function),
      });
    });
  });

  describe('should resolve placeholders in array schema', () => {
    const schema = recursive(
      object({
        value: pipe(string(), transform(Number)),
        children: array(Recur),
      })
    );

    test('for valid nested input', () => {
      expect(
        schema['~run'](
          {
            value: {
              value: '1',
              children: [
                { value: '2', children: [] },
                { value: '3', children: [{ value: '4', children: [] }] },
              ],
            },
          },
          {}
        )
      ).toStrictEqual({
        typed: true,
        value: {
          value: 1,
          children: [
            { value: 2, children: [] },
            { value: 3, children: [{ value: 4, children: [] }] },
          ],
        },
      });
    });

    test('for invalid nested input', () => {
      const child = { value: 4, children: [] };
      const children = [child];
      const parent = { value: '3', children };
      const input = { value: '1', children: [parent] };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: false,
        value: {
          value: 1,
          children: [{ value: 3, children: [{ value: 4, children: [] }] }],
        },
        issues: [
          {
            kind: 'schema',
            type: 'string',
            input: 4,
            expected: 'string',
            received: '4',
            message: 'Invalid type: Expected string but received 4',
            requirement: undefined,
            path: [
              {
                type: 'object',
                origin: 'value',
                input,
                key: 'children',
                value: input.children,
              },
              {
                type: 'array',
                origin: 'value',
                input: input.children,
                key: 0,
                value: parent,
              },
              {
                type: 'object',
                origin: 'value',
                input: parent,
                key: 'children',
                value: children,
              },
              {
                type: 'array',
                origin: 'value',
                input: children,
                key: 0,
                value: child,
              },
              {
                type: 'object',
                origin: 'value',
                input: child,
                key: 'value',
                value: 4,
              },
            ],
            issues: undefined,
            lang: undefined,
            abortEarly: undefined,
            abortPipeEarly: undefined,
          },
        ],
      });
    });
  });

  describe('should resolve placeholders in record, map and set schemas', () => {
    const schema = recursive(
      object({
        name: string(),
        record: optional(record(string(), Recur)),
        map: optional(map(string(), Recur)),
        set: optional(set(Recur)),
      })
    );

    test('for valid nested input', () => {
      const input = {
        name: 'root',
        record: {
          foo: {
            name: 'foo',
            map: new Map([['bar', { name: 'bar', set: new Set([]) }]]),
          },
        },
        set: new Set([{ name: 'baz', record: {} }]),
      };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for invalid nested input', () => {
      const dataset = schema['~run'](
        {
          value: {
            name: 'root',
            record: { foo: { name: 1 } },
            map: new Map([['bar', { name: 'bar', set: new Set([{}]) }]]),
            set: new Set([{ name: 'baz', record: { qux: null } }]),
          },
        },
        {}
      );
      expect(dataset.typed).toBe(false);
      expect(getIssueKeys(dataset)).toStrictEqual([
        ['record', 'foo', 'name'],
        ['map', 'bar', 'set', null, 'name'],
        ['set', null, 'record', 'qux'],
      ]);
    });
  });

  describe('should resolve placeholders in other schemas', () => {
    const schema = recursive(
      object({
        tuple: optional(tuple([string(), Recur])),
        lazy: optional(lazy(() => Recur)),
        variant: optional(
          variant('type', [
            object({ type: literal('node'), next: Recur }),
            object({ type: literal('leaf') }),
          ])
        ),
      })
    );

    test('for valid nested input', () => {
      const input = {
        tuple: ['foo', { lazy: { variant: { type: 'node', next: {} } } }],
        variant: { type: 'leaf' },
      };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for invalid nested input', () => {
      const dataset = schema['~run'](
        {
          value: {
            tuple: ['foo', { lazy: { variant: { type: 'node', next: 1 } } }],
          },
        },
        {}
      );
      expect(getIssueKeys(dataset)).toStrictEqual([
        ['tuple', 1, 'lazy', 'variant', 'next'],
      ]);
    });
  });

  describe('should resolve placeholders at top level of union', () => {
    const schema = recursive(union([number(), array(Recur)]));

    test('for valid nested input', () => {
      const input = [1, [2, [3, []]], []];
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for invalid nested input', () => {
      const dataset = schema['~run']({ value: [1, [2, ['3']]] }, {});
      expect(dataset.typed).toBe(false);
      expect(dataset.issues).toHaveLength(1);
      expect(dataset.issues![0].type).toBe('union');
    });
  });

  describe('should compose with pipe', () => {
    test('for recursive schema inside of pipe', () => {
      const schema = pipe(
        recursive(object({ value: number(), children: array(Recur) })),
        transform((input) => input.children.length)
      );
      expect(
        schema['~run'](
          {
            value: {
              value: 1,
              children: [
                { value: 2, children: [] },
                { value: 3, children: [{ value: 4, children: [] }] },
              ],
            },
          },
          {}
        )
      ).toStrictEqual({ typed: true, value: 2 });
    });

    test('for pipe inside of recursive schema', () => {
      const schema = recursive(
        pipe(
          object({ value: number(), children: array(Recur) }),
          transform((input) => ({
            total: input.value * 10,
            children: input.children,
          }))
        )
      );
      expect(
        schema['~run'](
          {
            value: {
              value: 1,
              children: [
                { value: 2, children: [] },
                { value: 3, children: [{ value: 4, children: [] }] },
              ],
            },
          },
          {}
        )
      ).toStrictEqual({
        typed: true,
        value: {
          total: 10,
          children: [
            { total: 20, children: [] },
            { total: 30, children: [{ total: 40, children: [] }] },
          ],
        },
      });
    });
  });

  describe('should compose with intersect', () => {
    const schema = recursive(
      intersect([object({ id: string() }), object({ children: array(Recur) })])
    );

    test('for valid nested input', () => {
      const input = {
        id: '1',
        children: [{ id: '2', children: [{ id: '3', children: [] }] }],
      };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for invalid nested input', () => {
      const dataset = schema['~run'](
        {
          value: {
            id: '1',
            children: [{ id: '2', children: [{ children: [] }] }],
          },
        },
        {}
      );
      expect(getIssueKeys(dataset)).toStrictEqual([
        ['children', 0, 'children', 0, 'id'],
      ]);
    });
  });

  describe('should resolve placeholders to closest recursive schema', () => {
    const schema = recursive(
      object({
        tag: recursive(object({ name: string(), parent: optional(Recur) })),
        children: array(Recur),
      })
    );

    test('for valid nested input', () => {
      const input = {
        tag: { name: 'foo', parent: { name: 'bar' } },
        children: [{ tag: { name: 'baz' }, children: [] }],
      };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for invalid nested input', () => {
      const dataset = schema['~run'](
        {
          value: {
            tag: { name: 'foo', parent: { tag: { name: 'bar' } } },
            children: [{ tag: { name: 'baz' }, children: [{}] }],
          },
        },
        {}
      );
      expect(getIssueKeys(dataset)).toStrictEqual([
        ['tag', 'parent', 'name'],
        ['children', 0, 'children', 0, 'tag'],
        ['children', 0, 'children', 0, 'children'],
      ]);
    });
  });

  describe('should pass configuration to nested schemas', () => {
    const schema = recursive(
      object({ name: string(), children: array(Recur) })
    );
    const input = {
      name: 1,
      children: [{ name: 2, children: [] }],
    };

    test('for abort early', () => {
      expect(
        getIssueKeys(schema['~run']({ value: input }, { abortEarly: true }))
      ).toStrictEqual([['name']]);
      expect(getIssueKeys(schema['~run']({ value: input }, {}))).toStrictEqual([
        ['name'],
        ['children', 0, 'name'],
      ]);
    });

    test('for local configuration', () => {
      const dataset = config(schema, { lang: 'de' })['~run'](
        { value: input },
        {}
      );
      expect(dataset.issues?.map((issue) => issue.lang)).toStrictEqual([
        'de',
        'de',
      ]);
    });

    test('for local message', () => {
      const dataset = message(schema, 'foo')['~run']({ value: input }, {});
      expect(dataset.issues?.map((issue) => issue.message)).toStrictEqual([
        'foo',
        'foo',
      ]);
    });

    test('without mutating configuration', () => {
      const config: Config<BaseIssue<unknown>> = { abortEarly: false };
      schema['~run']({ value: input }, config);
      expect(config).toStrictEqual({ abortEarly: false });
    });
  });

  test('should support Standard Schema validation', () => {
    const schema = recursive(
      object({ name: string(), children: array(Recur) })
    );
    const input = { name: 'foo', children: [{ name: 'bar', children: [] }] };
    expect(schema['~standard'].validate(input)).toStrictEqual({
      typed: true,
      value: input,
    });
  });

  test('should keep non-recursive schemas unchanged', () => {
    const schema: BaseSchema<unknown, unknown, BaseIssue<unknown>> = recursive(
      union([string(), null_()])
    );
    expect(schema['~run']({ value: 'foo' }, {})).toStrictEqual({
      typed: true,
      value: 'foo',
    });
    expect(schema['~run']({ value: 1 }, {}).typed).toBe(false);
  });
});

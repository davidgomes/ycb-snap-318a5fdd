import { describe, expect, test } from 'vitest';
import { checkAsync, transform } from '../../actions/index.ts';
import {
  array,
  arrayAsync,
  intersectAsync,
  mapAsync,
  number,
  object,
  objectAsync,
  optional,
  optionalAsync,
  recordAsync,
  setAsync,
  string,
  unionAsync,
} from '../../schemas/index.ts';
import type { BaseIssue, Config, OutputDataset } from '../../types/index.ts';
import { pipe, pipeAsync } from '../pipe/index.ts';
import { Recur, recursive } from './recursive.ts';
import { recursiveAsync } from './recursiveAsync.ts';

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

describe('recursiveAsync', () => {
  describe('should return schema object', () => {
    test('for sync schema', () => {
      const schema = object({ key: string(), children: array(Recur) });
      expect(recursiveAsync(schema)).toStrictEqual({
        ...schema,
        async: true,
        '~standard': {
          version: 1,
          vendor: 'valibot',
          validate: expect.any(Function),
        },
        '~run': expect.any(Function),
      });
    });

    test('for async schema', () => {
      const schema = objectAsync({
        key: string(),
        children: arrayAsync(Recur),
      });
      expect(recursiveAsync(schema)).toStrictEqual({
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

  describe('should resolve placeholders in async array schema', () => {
    const schema = recursiveAsync(
      objectAsync({
        value: pipeAsync(
          string(),
          checkAsync(async (input) => input !== 'invalid'),
          transform(Number)
        ),
        children: arrayAsync(Recur),
      })
    );

    test('for valid nested input', async () => {
      expect(
        await schema['~run'](
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

    test('for invalid nested input', async () => {
      const child = { value: 'invalid', children: [] };
      const children = [child];
      const parent = { value: '3', children };
      const input = { value: '1', children: [parent] };
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: false,
        value: {
          value: 1,
          children: [
            { value: 3, children: [{ value: 'invalid', children: [] }] },
          ],
        },
        issues: [
          {
            kind: 'validation',
            type: 'check',
            input: 'invalid',
            expected: null,
            received: '"invalid"',
            message: 'Invalid input: Received "invalid"',
            requirement: expect.any(Function),
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
                value: 'invalid',
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

  describe('should resolve placeholders in async record, map and set schemas', () => {
    const schema = recursiveAsync(
      objectAsync({
        name: pipeAsync(
          string(),
          checkAsync(async (input) => input !== 'invalid')
        ),
        record: optionalAsync(recordAsync(string(), Recur)),
        map: optionalAsync(mapAsync(string(), Recur)),
        set: optionalAsync(setAsync(Recur)),
      })
    );

    test('for valid nested input', async () => {
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
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for invalid nested input', async () => {
      const dataset = await schema['~run'](
        {
          value: {
            name: 'root',
            record: { foo: { name: 'invalid' } },
            map: new Map([
              ['bar', { name: 'bar', set: new Set([{ name: 'invalid' }]) }],
            ]),
            set: new Set([{ name: 'baz', record: { qux: null } }]),
          },
        },
        {}
      );
      expect(getIssueKeys(dataset)).toStrictEqual([
        ['record', 'foo', 'name'],
        ['map', 'bar', 'set', null, 'name'],
        ['set', null, 'record', 'qux'],
      ]);
    });
  });

  describe('should resolve placeholders of sync schema', () => {
    const schema = recursiveAsync(
      object({ name: string(), children: optional(array(Recur)) })
    );

    test('for valid nested input', async () => {
      const input = { name: 'foo', children: [{ name: 'bar' }] };
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for invalid nested input', async () => {
      const dataset = await schema['~run'](
        { value: { name: 'foo', children: [{ name: 1 }] } },
        {}
      );
      expect(getIssueKeys(dataset)).toStrictEqual([['children', 0, 'name']]);
    });
  });

  describe('should resolve placeholders at top level of union', () => {
    const schema = recursiveAsync(
      unionAsync([
        pipeAsync(
          number(),
          checkAsync(async (input) => input > 0)
        ),
        arrayAsync(Recur),
      ])
    );

    test('for valid nested input', async () => {
      const input = [1, [2, [3, []]], []];
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for invalid nested input', async () => {
      const dataset = await schema['~run']({ value: [1, [2, [-3]]] }, {});
      expect(dataset.issues?.map((issue) => issue.type)).toStrictEqual([
        'check',
      ]);
      expect(getIssueKeys(dataset)).toStrictEqual([[1, 1, 0]]);
    });
  });

  describe('should compose with pipe', () => {
    test('for recursive schema inside of pipe', async () => {
      const schema = pipeAsync(
        recursiveAsync(
          objectAsync({ value: number(), children: arrayAsync(Recur) })
        ),
        transform((input) => input.children.length)
      );
      expect(
        await schema['~run'](
          {
            value: {
              value: 1,
              children: [
                { value: 2, children: [] },
                { value: 3, children: [] },
              ],
            },
          },
          {}
        )
      ).toStrictEqual({ typed: true, value: 2 });
    });

    test('for pipe inside of recursive schema', async () => {
      const schema = recursiveAsync(
        pipeAsync(
          objectAsync({ value: number(), children: arrayAsync(Recur) }),
          checkAsync(async (input) => input.value > 0),
          transform((input) => ({
            total: input.value * 10,
            children: input.children,
          }))
        )
      );
      expect(
        await schema['~run'](
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
    const schema = recursiveAsync(
      intersectAsync([
        objectAsync({
          id: pipeAsync(
            string(),
            checkAsync(async (input) => input !== 'invalid')
          ),
        }),
        objectAsync({ children: arrayAsync(Recur) }),
      ])
    );

    test('for valid nested input', async () => {
      const input = {
        id: '1',
        children: [{ id: '2', children: [{ id: '3', children: [] }] }],
      };
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for invalid nested input', async () => {
      const dataset = await schema['~run'](
        {
          value: {
            id: '1',
            children: [
              { id: '2', children: [{ id: 'invalid', children: [] }] },
            ],
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
    const schema = recursiveAsync(
      objectAsync({
        tag: recursive(object({ name: string(), parent: optional(Recur) })),
        children: arrayAsync(Recur),
      })
    );

    test('for valid nested input', async () => {
      const input = {
        tag: { name: 'foo', parent: { name: 'bar' } },
        children: [{ tag: { name: 'baz' }, children: [] }],
      };
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for invalid nested input', async () => {
      const dataset = await schema['~run'](
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
    const schema = recursiveAsync(
      objectAsync({ name: string(), children: arrayAsync(Recur) })
    );
    const input = {
      name: 1,
      children: [{ name: 2, children: [] }],
    };

    test('for abort early', async () => {
      expect(
        getIssueKeys(
          await schema['~run']({ value: input }, { abortEarly: true })
        )
      ).toStrictEqual([['name']]);
      expect(
        getIssueKeys(await schema['~run']({ value: input }, {}))
      ).toStrictEqual([['name'], ['children', 0, 'name']]);
    });

    test('without mutating configuration', async () => {
      const config: Config<BaseIssue<unknown>> = { abortEarly: false };
      await schema['~run']({ value: input }, config);
      expect(config).toStrictEqual({ abortEarly: false });
    });
  });

  test('should support Standard Schema validation', async () => {
    const schema = recursiveAsync(
      objectAsync({ name: string(), children: arrayAsync(Recur) })
    );
    const input = { name: 'foo', children: [{ name: 'bar', children: [] }] };
    expect(await schema['~standard'].validate(input)).toStrictEqual({
      typed: true,
      value: input,
    });
  });

  test('should keep sync recursive schemas working inside of async schema', async () => {
    const schema = objectAsync({
      tree: recursive(
        pipe(
          object({ value: number(), children: array(Recur) }),
          transform((input) => input.children.length)
        )
      ),
    });
    expect(
      await schema['~run'](
        {
          value: { tree: { value: 1, children: [{ value: 2, children: [] }] } },
        },
        {}
      )
    ).toStrictEqual({ typed: true, value: { tree: 1 } });
  });
});

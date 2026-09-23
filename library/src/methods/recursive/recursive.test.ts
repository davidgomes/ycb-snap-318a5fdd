import { describe, expect, test } from 'vitest';
import { transform, transformAsync } from '../../actions/index.ts';
import {
  array,
  arrayAsync,
  intersect,
  map,
  number,
  object,
  objectAsync,
  optional,
  record,
  set,
  string,
} from '../../schemas/index.ts';
import { parse } from '../parse/index.ts';
import { pipe } from '../pipe/pipe.ts';
import { pipeAsync } from '../pipe/pipeAsync.ts';
import { safeParse } from '../safeParse/index.ts';
import { Recur, type RecurSchema } from './recur.ts';
import { recursive, type RecursiveSchema } from './recursive.ts';
import { recursiveAsync } from './recursiveAsync.ts';

describe('recursive', () => {
  test('should return schema object', () => {
    const wrapped = object({ value: string(), child: optional(Recur) });
    expect(recursive(wrapped)).toStrictEqual({
      kind: 'schema',
      type: 'recursive',
      reference: recursive,
      expects: 'unknown',
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

  test('should return recur placeholder', () => {
    expect(Recur).toMatchObject({
      kind: 'schema',
      type: 'recur',
      expects: 'recursive',
      async: false,
    } satisfies Partial<RecurSchema>);
  });

  test('should parse nested arrays', () => {
    const schema = recursive(
      object({
        value: string(),
        children: array(Recur),
      })
    );
    expect(
      parse(schema, {
        value: 'a',
        children: [
          { value: 'b', children: [] },
          { value: 'c', children: [{ value: 'd', children: [] }] },
        ],
      })
    ).toStrictEqual({
      value: 'a',
      children: [
        { value: 'b', children: [] },
        { value: 'c', children: [{ value: 'd', children: [] }] },
      ],
    });
  });

  test('should parse record, map, and set values', () => {
    const schema = recursive(
      object({
        id: string(),
        kids: record(string(), Recur),
        lookup: map(string(), Recur),
        group: set(Recur),
      })
    );
    const child = {
      id: 'child',
      kids: {},
      lookup: new Map(),
      group: new Set(),
    };
    const input = {
      id: 'root',
      kids: { child },
      lookup: new Map([['child', child]]),
      group: new Set([child]),
    };
    expect(parse(schema, input)).toStrictEqual(input);
  });

  test('should compose through pipe and intersect', () => {
    const schema = recursive(
      pipe(
        intersect([
          object({ value: string() }),
          object({ child: optional(Recur) }),
        ]),
        transform((input) => ({
          len: input.value.length,
          child: input.child,
        }))
      )
    );
    expect(
      parse(schema, { value: 'abcd', child: { value: 'x' } })
    ).toStrictEqual({
      len: 4,
      child: { len: 1, child: undefined },
    });
  });

  test('should keep nested recursive schemas distinct', () => {
    const inner = recursive(
      object({
        n: number(),
        self: optional(Recur),
      })
    );
    const outer = recursive(
      object({
        label: string(),
        inner,
        child: optional(Recur),
      })
    );
    expect(
      parse(outer, {
        label: 'root',
        inner: { n: 1, self: { n: 2 } },
        child: { label: 'next', inner: { n: 3 } },
      })
    ).toStrictEqual({
      label: 'root',
      inner: { n: 1, self: { n: 2 } },
      child: {
        label: 'next',
        inner: { n: 3 },
      },
    });
  });

  test('should reject an unresolved placeholder at runtime', () => {
    const schema = object({ child: Recur });
    expect(
      schema['~run']({ value: { child: { child: null } } }, {})
    ).toMatchObject({
      typed: false,
      issues: [
        expect.objectContaining({
          kind: 'schema',
          type: 'recur',
          expected: 'recursive',
        }),
      ],
    });
    expect(safeParse(string(), 'ok').success).toBe(true);
  });
});

describe('recursiveAsync', () => {
  test('should parse async self-references', async () => {
    const schema = recursiveAsync(
      pipeAsync(
        objectAsync({
          value: string(),
          children: arrayAsync(Recur),
        }),
        transformAsync(async (input) => ({
          len: input.value.length,
          children: input.children,
        }))
      )
    );
    await expect(
      schema['~run'](
        {
          value: {
            value: 'root',
            children: [{ value: 'child', children: [] }],
          },
        },
        {}
      )
    ).resolves.toMatchObject({
      typed: true,
      value: {
        len: 4,
        children: [{ len: 5, children: [] }],
      },
    });
  });
});

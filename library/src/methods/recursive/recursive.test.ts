import { describe, expect, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  array,
  intersect,
  map,
  nullable,
  number,
  object,
  record,
  set,
  string,
} from '../../schemas/index.ts';
import type { FailureDataset } from '../../types/index.ts';
import { message } from '../message/index.ts';
import { pipe } from '../pipe/index.ts';
import { Recur, type RecurIssue, type RecurSchema } from './recur.ts';
import { recursive, type RecursiveSchema } from './recursive.ts';

describe('recursive', () => {
  test('should return schema object', () => {
    const wrapped = object({ child: nullable(Recur) });
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

  describe('should return dataset without issues', () => {
    test('for nullable child', () => {
      const schema = recursive(
        object({
          value: string(),
          child: nullable(Recur),
        })
      );
      const input = {
        value: 'a',
        child: { value: 'b', child: null },
      };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for array values', () => {
      const schema = recursive(
        object({
          value: string(),
          children: array(Recur),
        })
      );
      const input = {
        value: 'a',
        children: [{ value: 'b', children: [] }],
      };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for record values', () => {
      const schema = recursive(
        object({
          value: string(),
          children: record(string(), Recur),
        })
      );
      const input = {
        value: 'a',
        children: { b: { value: 'b', children: {} } },
      };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for map values', () => {
      const schema = recursive(
        object({
          value: string(),
          children: map(string(), Recur),
        })
      );
      const input = {
        value: 'a',
        children: new Map([['b', { value: 'b', children: new Map() }]]),
      };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for set values', () => {
      const schema = recursive(
        object({
          value: string(),
          children: set(Recur),
        })
      );
      const child = { value: 'b', children: new Set() };
      const input = { value: 'a', children: new Set([child]) };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for pipe transformation', () => {
      const schema = recursive(
        pipe(
          object({
            value: string(),
            child: nullable(Recur),
          }),
          transform((input) => ({
            length: input.value.length,
            child: input.child,
          }))
        )
      );
      expect(
        schema['~run'](
          { value: { value: 'ab', child: { value: 'c', child: null } } },
          {}
        )
      ).toStrictEqual({
        typed: true,
        value: { length: 2, child: { length: 1, child: null } },
      });
    });

    test('for intersect', () => {
      const schema = recursive(
        intersect([
          object({
            value: string(),
            child: nullable(Recur),
          }),
          object({ id: number() }),
        ])
      );
      const input = { value: 'a', child: null, id: 1 };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for message wrapper', () => {
      const schema = recursive(
        object({
          value: string(),
          child: message(nullable(Recur), 'nope'),
        })
      );
      const input = { value: 'a', child: { value: 'b', child: null } };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for nested recursive schemas', () => {
      const comment = recursive(
        object({
          text: string(),
          replies: array(Recur),
        })
      );
      const post = recursive(
        object({
          title: string(),
          comments: array(comment),
          related: nullable(Recur),
        })
      );
      const input = {
        title: 'Hello',
        comments: [
          { text: 'First', replies: [{ text: 'Reply', replies: [] }] },
        ],
        related: {
          title: 'Next',
          comments: [],
          related: null,
        },
      };
      expect(post['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });
  });

  describe('should return dataset with issues', () => {
    test('for unresolved placeholder', () => {
      const input = { child: null };
      expect(
        object({ child: Recur })['~run']({ value: input }, {})
      ).toStrictEqual({
        typed: false,
        value: input,
        issues: [
          {
            kind: 'schema',
            type: 'recur',
            input: null,
            expected: 'Recur',
            received: 'null',
            message: 'Invalid type: Expected Recur but received null',
            requirement: undefined,
            path: [
              {
                type: 'object',
                origin: 'value',
                input,
                key: 'child',
                value: null,
              },
            ],
            issues: undefined,
            lang: undefined,
            abortEarly: undefined,
            abortPipeEarly: undefined,
          },
        ],
      } satisfies FailureDataset<RecurIssue>);
    });

    test('for invalid nested value', () => {
      const schema = recursive(
        object({
          value: string(),
          child: nullable(Recur),
        })
      );
      const input = { value: 'a', child: { value: 1, child: null } };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: false,
        value: input,
        issues: [
          {
            kind: 'schema',
            type: 'string',
            input: 1,
            expected: 'string',
            received: '1',
            message: 'Invalid type: Expected string but received 1',
            requirement: undefined,
            path: [
              {
                type: 'object',
                origin: 'value',
                input,
                key: 'child',
                value: input.child,
              },
              {
                type: 'object',
                origin: 'value',
                input: input.child,
                key: 'value',
                value: 1,
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
});

describe('Recur', () => {
  test('should return schema object', () => {
    expect(Recur).toMatchObject({
      kind: 'schema',
      type: 'recur',
      expects: 'Recur',
      async: false,
    } satisfies Pick<RecurSchema, 'kind' | 'type' | 'expects' | 'async'>);
    expect(Recur.reference()).toBe(Recur);
  });

  test('should return dataset with issues', () => {
    expect(Recur['~run']({ value: 'foo' }, {})).toStrictEqual({
      typed: false,
      value: 'foo',
      issues: [
        {
          kind: 'schema',
          type: 'recur',
          input: 'foo',
          expected: 'Recur',
          received: '"foo"',
          message: 'Invalid type: Expected Recur but received "foo"',
          requirement: undefined,
          path: undefined,
          issues: undefined,
          lang: undefined,
          abortEarly: undefined,
          abortPipeEarly: undefined,
        },
      ],
    } satisfies FailureDataset<RecurIssue>);
  });
});

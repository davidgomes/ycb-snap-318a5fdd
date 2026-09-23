import { describe, expect, test } from 'vitest';
import { transformAsync } from '../../actions/index.ts';
import {
  arrayAsync,
  intersectAsync,
  mapAsync,
  nullableAsync,
  number,
  objectAsync,
  recordAsync,
  setAsync,
  string,
} from '../../schemas/index.ts';
import { pipeAsync } from '../pipe/index.ts';
import { Recur } from './recur.ts';
import { recursiveAsync, type RecursiveSchemaAsync } from './recursiveAsync.ts';

describe('recursiveAsync', () => {
  test('should return schema object', () => {
    const wrapped = objectAsync({ child: nullableAsync(Recur) });
    expect(recursiveAsync(wrapped)).toStrictEqual({
      kind: 'schema',
      type: 'recursive',
      reference: recursiveAsync,
      expects: 'Object',
      async: true,
      wrapped,
      '~standard': {
        version: 1,
        vendor: 'valibot',
        validate: expect.any(Function),
      },
      '~run': expect.any(Function),
    } satisfies RecursiveSchemaAsync<typeof wrapped>);
  });

  describe('should return dataset without issues', () => {
    test('for nullable child', async () => {
      const schema = recursiveAsync(
        objectAsync({
          value: string(),
          child: nullableAsync(Recur),
        })
      );
      const input = {
        value: 'a',
        child: { value: 'b', child: null },
      };
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for array values', async () => {
      const schema = recursiveAsync(
        objectAsync({
          value: string(),
          children: arrayAsync(Recur),
        })
      );
      const input = {
        value: 'a',
        children: [{ value: 'b', children: [] }],
      };
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for record values', async () => {
      const schema = recursiveAsync(
        objectAsync({
          value: string(),
          children: recordAsync(string(), Recur),
        })
      );
      const input = {
        value: 'a',
        children: { b: { value: 'b', children: {} } },
      };
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for map values', async () => {
      const schema = recursiveAsync(
        objectAsync({
          value: string(),
          children: mapAsync(string(), Recur),
        })
      );
      const input = {
        value: 'a',
        children: new Map([['b', { value: 'b', children: new Map() }]]),
      };
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for set values', async () => {
      const schema = recursiveAsync(
        objectAsync({
          value: string(),
          children: setAsync(Recur),
        })
      );
      const child = { value: 'b', children: new Set() };
      const input = { value: 'a', children: new Set([child]) };
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for pipe transformation', async () => {
      const schema = recursiveAsync(
        pipeAsync(
          objectAsync({
            value: string(),
            child: nullableAsync(Recur),
          }),
          transformAsync(async (input) => ({
            length: input.value.length,
            child: input.child,
          }))
        )
      );
      expect(
        await schema['~run'](
          { value: { value: 'ab', child: { value: 'c', child: null } } },
          {}
        )
      ).toStrictEqual({
        typed: true,
        value: { length: 2, child: { length: 1, child: null } },
      });
    });

    test('for intersect', async () => {
      const schema = recursiveAsync(
        intersectAsync([
          objectAsync({
            value: string(),
            child: nullableAsync(Recur),
          }),
          objectAsync({ id: number() }),
        ])
      );
      const input = { value: 'a', child: null, id: 1 };
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });
  });

  test('should return dataset with issues', async () => {
    const schema = recursiveAsync(
      objectAsync({
        value: string(),
        child: nullableAsync(Recur),
      })
    );
    const input = { value: 'a', child: { value: 1, child: null } };
    expect(await schema['~run']({ value: input }, {})).toStrictEqual({
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

import { describe, expect, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  arrayAsync,
  intersectAsync,
  mapAsync,
  objectAsync,
  recordAsync,
  setAsync,
  string,
} from '../../schemas/index.ts';
import { parseAsync } from '../parse/index.ts';
import { pipeAsync } from '../pipe/index.ts';
import { Recur } from './Recur.ts';
import { recursiveAsync, type RecursiveSchemaAsync } from './recursiveAsync.ts';

describe('recursiveAsync', () => {
  const wrapped = objectAsync({
    name: string(),
    children: arrayAsync(Recur),
  });
  type Wrapped = typeof wrapped;

  test('should return schema object', () => {
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
    } satisfies RecursiveSchemaAsync<Wrapped>);
  });

  describe('should return dataset without issues', () => {
    test('for nested object with array values', async () => {
      const schema = recursiveAsync(
        objectAsync({
          name: string(),
          children: arrayAsync(Recur),
        })
      );
      const input = {
        name: 'root',
        children: [
          { name: 'child', children: [] },
          {
            name: 'branch',
            children: [{ name: 'leaf', children: [] }],
          },
        ],
      };
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for record values', async () => {
      const schema = recursiveAsync(recordAsync(string(), Recur));
      const input = { a: { b: {} } };
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for map values', async () => {
      const schema = recursiveAsync(mapAsync(string(), Recur));
      const input = new Map([['child', new Map()]]);
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for set values', async () => {
      const schema = recursiveAsync(setAsync(Recur));
      const input = new Set([new Set()]);
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for pipe composition', async () => {
      const schema = recursiveAsync(
        pipeAsync(
          objectAsync({
            name: string(),
            children: arrayAsync(Recur),
          }),
          transform((input) => ({
            ...input,
            name: input.name.trim(),
          }))
        )
      );
      expect(
        await schema['~run'](
          {
            value: {
              name: ' root ',
              children: [{ name: ' child ', children: [] }],
            },
          },
          {}
        )
      ).toStrictEqual({
        typed: true,
        value: {
          name: 'root',
          children: [{ name: 'child', children: [] }],
        },
      });
    });

    test('for intersect composition', async () => {
      const schema = recursiveAsync(
        intersectAsync([
          objectAsync({ name: string() }),
          objectAsync({ children: arrayAsync(Recur) }),
        ])
      );
      const input = {
        name: 'root',
        children: [{ name: 'child', children: [] }],
      };
      expect(await schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });
  });

  describe('should return dataset with issues', () => {
    test('for invalid nested value', async () => {
      const schema = recursiveAsync(
        objectAsync({
          name: string(),
          children: arrayAsync(Recur),
        })
      );
      const result = await schema['~run'](
        { value: { name: 'root', children: [{ name: 123, children: [] }] } },
        {}
      );
      expect(result.typed).toBe(false);
      expect(result.issues).toBeDefined();
    });
  });

  test('should parse nested tree', async () => {
    const schema = recursiveAsync(
      objectAsync({
        name: string(),
        children: arrayAsync(Recur),
      })
    );
    const input = {
      name: 'root',
      children: [{ name: 'leaf', children: [] }],
    };
    expect(await parseAsync(schema, input)).toStrictEqual(input);
  });
});

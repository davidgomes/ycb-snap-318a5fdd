import { describe, expect, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  array,
  date,
  intersect,
  map,
  number,
  object,
  record,
  set,
  string,
} from '../../schemas/index.ts';
import { parse } from '../parse/index.ts';
import { pipe } from '../pipe/index.ts';
import { Recur } from './Recur.ts';
import { recursive, type RecursiveSchema } from './recursive.ts';

describe('recursive', () => {
  const wrapped = object({
    name: string(),
    children: array(Recur),
  });
  type Wrapped = typeof wrapped;

  test('should return schema object', () => {
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
    } satisfies RecursiveSchema<Wrapped>);
  });

  describe('should return dataset without issues', () => {
    test('for nested object with array values', () => {
      const schema = recursive(
        object({
          name: string(),
          children: array(Recur),
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
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for nested object with date values', () => {
      const schema = recursive(
        object({
          name: string(),
          createdAt: date(),
          children: array(Recur),
        })
      );
      const createdAt = new Date('2024-01-01T00:00:00.000Z');
      const input = {
        name: 'root',
        createdAt,
        children: [{ name: 'leaf', createdAt, children: [] }],
      };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for record values', () => {
      const schema = recursive(record(string(), Recur));
      const input = { a: { b: {} } };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for map values', () => {
      const schema = recursive(map(string(), Recur));
      const input = new Map([['child', new Map()]]);
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for set values', () => {
      const schema = recursive(set(Recur));
      const input = new Set([new Set()]);
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });

    test('for pipe composition', () => {
      const schema = recursive(
        pipe(
          object({
            name: string(),
            children: array(Recur),
          }),
          transform((input) => ({
            ...input,
            name: input.name.trim(),
          }))
        )
      );
      expect(
        schema['~run'](
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

    test('for intersect composition', () => {
      const schema = recursive(
        intersect([
          object({ name: string() }),
          object({ children: array(Recur) }),
        ])
      );
      const input = {
        name: 'root',
        children: [{ name: 'child', children: [] }],
      };
      expect(schema['~run']({ value: input }, {})).toStrictEqual({
        typed: true,
        value: input,
      });
    });
  });

  describe('should return dataset with issues', () => {
    test('for invalid nested value', () => {
      const schema = recursive(
        object({
          name: string(),
          children: array(Recur),
        })
      );
      const result = schema['~run'](
        { value: { name: 'root', children: [{ name: 123, children: [] }] } },
        {}
      );
      expect(result.typed).toBe(false);
      expect(result.issues).toBeDefined();
    });

    test('for invalid root type', () => {
      const schema = recursive(
        object({
          name: string(),
          children: array(Recur),
        })
      );
      const result = schema['~run']({ value: 'foo' }, {});
      expect(result.typed).toBe(false);
      expect(result.issues).toBeDefined();
    });
  });

  test('should parse nested tree', () => {
    const schema = recursive(
      object({
        name: string(),
        count: number(),
        children: array(Recur),
      })
    );
    const input = {
      name: 'root',
      count: 1,
      children: [{ name: 'leaf', count: 2, children: [] }],
    };
    expect(parse(schema, input)).toStrictEqual(input);
  });
});

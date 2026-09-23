import { describe, expect, test } from 'vitest';
import { transform } from '../../actions/index.ts';
import {
  array,
  intersect,
  map,
  number,
  object,
  optional,
  record,
  set,
  string,
} from '../../schemas/index.ts';
import { message } from '../message/index.ts';
import { parse } from '../parse/index.ts';
import { pipe } from '../pipe/index.ts';
import { Recur } from './recur.ts';
import { recursive } from './recursive.ts';

describe('recursive', () => {
  test('should return schema object', () => {
    const wrapped = object({ child: optional(Recur) });
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
    });
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
    const recordSchema = recursive(record(string(), Recur));
    expect(parse(recordSchema, { a: { b: {} } })).toStrictEqual({
      a: { b: {} },
    });

    const mapSchema = recursive(map(string(), Recur));
    const mapInput = new Map<unknown, unknown>([
      ['a', new Map([['b', new Map()]])],
    ]);
    expect(parse(mapSchema, mapInput)).toStrictEqual(mapInput);

    const setSchema = recursive(set(Recur));
    const setInput = new Set([new Set()]);
    expect(parse(setSchema, setInput)).toStrictEqual(setInput);
  });

  test('should parse pipe and intersect compositions', () => {
    const piped = recursive(
      pipe(
        object({
          name: string(),
          children: array(Recur),
        }),
        transform((input) => ({
          label: input.name,
          count: input.children.length,
        }))
      )
    );
    expect(
      parse(piped, {
        name: 'root',
        children: [{ name: 'child', children: [] }],
      })
    ).toStrictEqual({ label: 'root', count: 1 });

    const intersected = recursive(
      intersect([
        object({ id: string(), children: array(Recur) }),
        object({ name: number() }),
      ])
    );
    expect(
      parse(intersected, {
        id: '1',
        name: 2,
        children: [{ id: '3', name: 4, children: [] }],
      })
    ).toStrictEqual({
      id: '1',
      name: 2,
      children: [{ id: '3', name: 4, children: [] }],
    });
  });

  test('should keep nested recursive schemas distinct', () => {
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
        related: optional(Recur),
      })
    );
    expect(
      parse(post, {
        title: 'Hello',
        comments: [{ text: 'Hi', replies: [{ text: 'Hey', replies: [] }] }],
        related: {
          title: 'Next',
          comments: [],
        },
      })
    ).toStrictEqual({
      title: 'Hello',
      comments: [{ text: 'Hi', replies: [{ text: 'Hey', replies: [] }] }],
      related: {
        title: 'Next',
        comments: [],
      },
    });
  });

  test('should resolve placeholders through message config cloning', () => {
    const schema = recursive(
      message(
        object({
          name: string(),
          child: optional(Recur),
        }),
        'Invalid node.'
      )
    );
    expect(
      parse(schema, { name: 'root', child: { name: 'leaf' } })
    ).toStrictEqual({ name: 'root', child: { name: 'leaf' } });
    expect(() => parse(schema, { name: 1 })).toThrowError('Invalid node.');
  });
});

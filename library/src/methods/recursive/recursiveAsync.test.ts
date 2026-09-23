import { describe, expect, test } from 'vitest';
import { checkAsync } from '../../actions/index.ts';
import {
  arrayAsync,
  mapAsync,
  objectAsync,
  recordAsync,
  setAsync,
  string,
} from '../../schemas/index.ts';
import { parseAsync } from '../parse/index.ts';
import { pipeAsync } from '../pipe/index.ts';
import { Recur } from './recur.ts';
import { recursiveAsync } from './recursiveAsync.ts';

describe('recursiveAsync', () => {
  test('should return schema object', () => {
    const wrapped = objectAsync({ child: Recur });
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
    });
  });

  test('should parse nested async arrays', async () => {
    const schema = recursiveAsync(
      objectAsync({
        value: string(),
        children: arrayAsync(Recur),
      })
    );
    expect(
      await parseAsync(schema, {
        value: 'a',
        children: [{ value: 'b', children: [] }],
      })
    ).toStrictEqual({
      value: 'a',
      children: [{ value: 'b', children: [] }],
    });
  });

  test('should parse async record, map, and set values', async () => {
    const recordSchema = recursiveAsync(recordAsync(string(), Recur));
    expect(await parseAsync(recordSchema, { a: {} })).toStrictEqual({ a: {} });

    const mapSchema = recursiveAsync(mapAsync(string(), Recur));
    const mapInput = new Map([['a', new Map()]]);
    expect(await parseAsync(mapSchema, mapInput)).toStrictEqual(mapInput);

    const setSchema = recursiveAsync(setAsync(Recur));
    const setInput = new Set([new Set()]);
    expect(await parseAsync(setSchema, setInput)).toStrictEqual(setInput);
  });

  test('should parse async pipes', async () => {
    const schema = recursiveAsync(
      pipeAsync(
        objectAsync({
          name: string(),
          children: arrayAsync(Recur),
        }),
        checkAsync(async (input) => input.name.length > 0)
      )
    );
    expect(
      await parseAsync(schema, {
        name: 'root',
        children: [{ name: 'child', children: [] }],
      })
    ).toStrictEqual({
      name: 'root',
      children: [{ name: 'child', children: [] }],
    });
  });
});

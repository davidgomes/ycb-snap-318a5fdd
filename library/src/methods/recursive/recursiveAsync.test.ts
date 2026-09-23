import { describe, expect, test } from 'vitest';
import { checkAsync, transformAsync } from '../../actions/index.ts';
import {
  arrayAsync,
  intersectAsync,
  mapAsync,
  objectAsync,
  optionalAsync,
  recordAsync,
  setAsync,
  string,
} from '../../schemas/index.ts';
import { parseAsync } from '../parse/index.ts';
import { pipeAsync } from '../pipe/index.ts';
import { safeParseAsync } from '../safeParse/index.ts';
import { Recur } from './recur.ts';
import { recursiveAsync, type RecursiveSchemaAsync } from './recursiveAsync.ts';

describe('recursiveAsync', () => {
  const wrapped = objectAsync({
    value: string(),
    children: arrayAsync(Recur),
  });

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
    } satisfies RecursiveSchemaAsync<typeof wrapped>);
  });

  test('should parse async object and array recursion', async () => {
    const schema = recursiveAsync(wrapped);
    const input = {
      value: 'root',
      children: [{ value: 'child', children: [] }],
    };
    expect(await parseAsync(schema, input)).toStrictEqual(input);
    expect(await safeParseAsync(schema, input)).toStrictEqual({
      typed: true,
      success: true,
      output: input,
      issues: undefined,
    });
  });

  test('should parse async record, map, and set value recursion', async () => {
    const recordSchema = recursiveAsync(
      recordAsync(
        string(),
        objectAsync({ id: string(), child: optionalAsync(Recur) })
      )
    );
    const recordInput = { a: { id: 'a', child: { b: { id: 'b' } } } };
    expect(await parseAsync(recordSchema, recordInput)).toStrictEqual(
      recordInput
    );

    const mapSchema = recursiveAsync(
      mapAsync(
        string(),
        objectAsync({ id: string(), next: optionalAsync(Recur) })
      )
    );
    const mapInput = new Map([['a', { id: 'a', next: new Map() }]]);
    expect(await parseAsync(mapSchema, mapInput)).toStrictEqual(mapInput);

    const setSchema = recursiveAsync(
      setAsync(objectAsync({ id: string(), nested: optionalAsync(Recur) }))
    );
    const setInput = new Set([
      { id: 'root', nested: new Set([{ id: 'leaf' }]) },
    ]);
    expect(await parseAsync(setSchema, setInput)).toStrictEqual(setInput);
  });

  test('should compose through pipeAsync and intersectAsync', async () => {
    const piped = recursiveAsync(
      pipeAsync(
        objectAsync({
          name: string(),
          nodes: arrayAsync(Recur),
        }),
        transformAsync(async (input) => ({
          ...input,
          count: input.nodes.length,
        }))
      )
    );
    expect(
      await parseAsync(piped, {
        name: 'root',
        nodes: [{ name: 'child', nodes: [] }],
      })
    ).toStrictEqual({
      name: 'root',
      count: 1,
      nodes: [{ name: 'child', count: 0, nodes: [] }],
    });

    const intersected = recursiveAsync(
      intersectAsync([
        objectAsync({ name: string() }),
        objectAsync({ child: optionalAsync(Recur) }),
      ])
    );
    const input = { name: 'root', child: { name: 'child' } };
    expect(await parseAsync(intersected, input)).toStrictEqual(input);
  });

  test('should isolate concurrent async recursion', async () => {
    const schema = recursiveAsync(
      pipeAsync(
        objectAsync({
          label: string(),
          child: optionalAsync(Recur),
        }),
        checkAsync(async () => {
          await Promise.resolve();
          return true;
        })
      )
    );
    const first = { label: 'a', child: { label: 'b' } };
    const second = {
      label: 'c',
      child: { label: 'd', child: { label: 'e' } },
    };
    await expect(
      Promise.all([parseAsync(schema, first), parseAsync(schema, second)])
    ).resolves.toStrictEqual([first, second]);
  });

  test('should return nested issues for invalid async input', async () => {
    const schema = recursiveAsync(wrapped);
    const dataset = await schema['~run'](
      { value: { value: 'ok', children: [{ value: 1, children: [] }] } },
      { lang: 'en' }
    );
    expect(dataset.issues?.[0]).toMatchObject({
      type: 'string',
      lang: 'en',
    });
  });
});

import { describe, expect, test } from 'vitest';
import { array, object } from '../../schemas/index.ts';
import { Recur, type RecurSchema } from './recur.ts';

describe('Recur', () => {
  test('should be schema object', () => {
    expect(Recur).toStrictEqual({
      kind: 'schema',
      type: 'recur',
      reference: expect.any(Function),
      expects: 'unknown',
      async: false,
      '~standard': {
        version: 1,
        vendor: 'valibot',
        validate: expect.any(Function),
      },
      '~run': expect.any(Function),
    } satisfies RecurSchema);
    expect(Recur.reference()).toBe(Recur);
  });

  test('should throw error if not resolved', () => {
    const message =
      'Unresolved Recur placeholder. Wrap the schema with recursive(...) or recursiveAsync(...) first.';
    expect(() => Recur['~run']({ value: 'foo' }, {})).toThrowError(message);
    expect(() =>
      object({ children: array(Recur) })['~run'](
        { value: { children: [{}] } },
        {}
      )
    ).toThrowError(message);
  });
});

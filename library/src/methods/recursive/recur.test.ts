import { describe, expect, test } from 'vitest';
import { Recur } from './recur.ts';

describe('Recur', () => {
  test('should return schema object', () => {
    expect(Recur).toStrictEqual({
      kind: 'schema',
      type: 'recur',
      reference: Recur.reference,
      expects: 'unknown',
      async: false,
      '~standard': {
        version: 1,
        vendor: 'valibot',
        validate: expect.any(Function),
      },
      '~run': expect.any(Function),
    });
    expect(Recur.reference()).toBe(Recur);
  });

  test('should throw when parsed without a recursive wrapper', () => {
    expect(() => Recur['~run']({ value: {} }, {})).toThrowError(
      'Unresolved Recur placeholder: wrap the schema with recursive or recursiveAsync before parsing.'
    );
  });
});

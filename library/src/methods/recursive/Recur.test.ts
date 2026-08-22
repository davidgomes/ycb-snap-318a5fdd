import { describe, expect, test } from 'vitest';
import { Recur, type RecurSchema } from './Recur.ts';

describe('Recur', () => {
  test('should return schema object', () => {
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
  });

  test('should return itself from reference', () => {
    expect(Recur.reference()).toBe(Recur);
  });

  test('should fail when placeholder is unresolved', () => {
    const result = Recur['~run']({ value: 'foo' }, {});
    expect(result).toStrictEqual({
      typed: false,
      value: 'foo',
      issues: [
        {
          kind: 'schema',
          type: 'recur',
          input: 'foo',
          expected: 'unknown',
          received: '"foo"',
          message: 'Invalid type: Expected unknown but received "foo"',
          requirement: undefined,
          path: undefined,
          issues: undefined,
          lang: undefined,
          abortEarly: undefined,
          abortPipeEarly: undefined,
        },
      ],
    });
  });
});

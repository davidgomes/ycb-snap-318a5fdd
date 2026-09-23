import { describe, expectTypeOf, test } from 'vitest';
import type { InferInput, InferIssue, InferOutput } from '../../types/index.ts';
import { Recur, type RecurSchema } from './recur.ts';
import type { RecurType } from './types.ts';

describe('Recur', () => {
  test('should be schema object', () => {
    expectTypeOf(Recur).toEqualTypeOf<RecurSchema>();
  });

  describe('should infer correct types', () => {
    test('of input', () => {
      expectTypeOf<InferInput<RecurSchema>>().toEqualTypeOf<RecurType>();
    });

    test('of output', () => {
      expectTypeOf<InferOutput<RecurSchema>>().toEqualTypeOf<RecurType>();
    });

    test('of issue', () => {
      expectTypeOf<InferIssue<RecurSchema>>().toEqualTypeOf<never>();
    });
  });
});

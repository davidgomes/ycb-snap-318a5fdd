import { describe, expectTypeOf, test } from 'vitest';
import type { InferInput, InferIssue, InferOutput } from '../../types/index.ts';
import { Recur, type RecurIssue, type RecurSchema } from './Recur.ts';
import type { RecurPlaceholder } from './types.ts';

describe('Recur', () => {
  test('should be recur schema', () => {
    expectTypeOf(Recur).toEqualTypeOf<RecurSchema>();
  });

  describe('should infer correct types', () => {
    test('of input', () => {
      expectTypeOf<InferInput<RecurSchema>>().toEqualTypeOf<RecurPlaceholder>();
    });

    test('of output', () => {
      expectTypeOf<
        InferOutput<RecurSchema>
      >().toEqualTypeOf<RecurPlaceholder>();
    });

    test('of issue', () => {
      expectTypeOf<InferIssue<RecurSchema>>().toEqualTypeOf<RecurIssue>();
    });
  });
});

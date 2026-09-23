import { describe, expectTypeOf, test } from 'vitest';
import { checkAsync, type CheckIssue, transform } from '../../actions/index.ts';
import {
  array,
  arrayAsync,
  type ArrayIssue,
  intersectAsync,
  mapAsync,
  number,
  object,
  objectAsync,
  type ObjectIssue,
  optionalAsync,
  recordAsync,
  setAsync,
  string,
  type StringIssue,
} from '../../schemas/index.ts';
import type { InferInput, InferIssue, InferOutput } from '../../types/index.ts';
import { pipeAsync } from '../pipe/index.ts';
import { Recur, recursive } from './recursive.ts';
import {
  recursiveAsync,
  type SchemaWithRecursiveAsync,
} from './recursiveAsync.ts';

describe('recursiveAsync', () => {
  describe('should return schema object', () => {
    test('for sync schema', () => {
      const schema = object({ key: string(), children: array(Recur) });
      expectTypeOf(recursiveAsync(schema)).toEqualTypeOf<
        SchemaWithRecursiveAsync<typeof schema>
      >();
    });

    test('for async schema', () => {
      const schema = objectAsync({
        key: string(),
        children: arrayAsync(Recur),
      });
      expectTypeOf(recursiveAsync(schema)).toEqualTypeOf<
        SchemaWithRecursiveAsync<typeof schema>
      >();
    });
  });

  describe('should infer correct types', () => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema = recursiveAsync(
      objectAsync({
        value: pipeAsync(
          string(),
          checkAsync(async () => true),
          transform(Number)
        ),
        children: arrayAsync(Recur),
      })
    );
    type Schema = typeof schema;
    interface Input {
      value: string;
      children: Input[];
    }
    interface Output {
      value: number;
      children: Output[];
    }

    test('of input', () => {
      expectTypeOf<InferInput<Schema>>().toEqualTypeOf<Input>();
    });

    test('of output', () => {
      expectTypeOf<InferOutput<Schema>>().toEqualTypeOf<Output>();
    });

    test('of issue', () => {
      expectTypeOf<InferIssue<Schema>>().toEqualTypeOf<
        ObjectIssue | StringIssue | CheckIssue<string> | ArrayIssue
      >();
    });

    test('of recursive positions', () => {
      expectTypeOf<InferInput<Schema>['children'][number]>().toEqualTypeOf<
        InferInput<Schema>
      >();
      expectTypeOf<InferOutput<Schema>['children'][number]>().toEqualTypeOf<
        InferOutput<Schema>
      >();
    });
  });

  test('should infer recursive record, map and set values', () => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema = recursiveAsync(
      objectAsync({
        record: optionalAsync(recordAsync(string(), Recur)),
        map: optionalAsync(mapAsync(string(), Recur)),
        set: optionalAsync(setAsync(Recur)),
      })
    );
    interface Output {
      record?: { [key: string]: Output } | undefined;
      map?: Map<string, Output> | undefined;
      set?: Set<Output> | undefined;
    }
    expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Output>();
    expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Output>();
  });

  test('should compose with pipe', () => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema = recursiveAsync(
      pipeAsync(
        objectAsync({ value: number(), children: arrayAsync(Recur) }),
        transform((input) => ({ sum: input.value, next: input.children }))
      )
    );
    interface Input {
      value: number;
      children: Input[];
    }
    interface Output {
      sum: number;
      next: Output[];
    }
    expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Input>();
    expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Output>();
  });

  test('should compose with intersect', () => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema = recursiveAsync(
      intersectAsync([
        objectAsync({ id: string() }),
        objectAsync({ children: arrayAsync(Recur) }),
      ])
    );
    interface Output {
      id: string;
      children: Output[];
    }
    expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Output>();
  });

  test('should resolve placeholders to closest recursive schema', () => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema = recursiveAsync(
      objectAsync({
        tag: recursive(object({ name: string(), parents: array(Recur) })),
        children: arrayAsync(Recur),
      })
    );
    interface Tag {
      name: string;
      parents: Tag[];
    }
    interface Output {
      tag: Tag;
      children: Output[];
    }
    expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Output>();
  });
});

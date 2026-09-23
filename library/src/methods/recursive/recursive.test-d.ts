import { describe, expectTypeOf, test } from 'vitest';
import { type Brand, brand, readonly, transform } from '../../actions/index.ts';
import {
  any,
  array,
  type ArrayIssue,
  custom,
  date,
  instance,
  intersect,
  lazy,
  literal,
  map,
  null_,
  nullable,
  number,
  type NumberIssue,
  object,
  type ObjectIssue,
  optional,
  record,
  set,
  string,
  type StringIssue,
  tuple,
  tupleWithRest,
  union,
  type UnionIssue,
  variant,
} from '../../schemas/index.ts';
import type { InferInput, InferIssue, InferOutput } from '../../types/index.ts';
import { pipe } from '../pipe/index.ts';
import {
  Recur,
  type RecurSchema,
  recursive,
  type SchemaWithRecursive,
} from './recursive.ts';
import type {
  HasRecur,
  NoUnresolvedRecur,
  RecurMarker,
  ResolveRecur,
  StripRecur,
  UnresolvedRecur,
} from './types.ts';

describe('Recur', () => {
  test('should return schema object', () => {
    expectTypeOf(Recur).toEqualTypeOf<RecurSchema>();
  });

  describe('should infer correct types', () => {
    test('of input', () => {
      expectTypeOf<InferInput<RecurSchema>>().toEqualTypeOf<RecurMarker>();
    });

    test('of output', () => {
      expectTypeOf<InferOutput<RecurSchema>>().toEqualTypeOf<RecurMarker>();
    });

    test('of issue', () => {
      expectTypeOf<InferIssue<RecurSchema>>().toEqualTypeOf<never>();
    });
  });
});

describe('recursive', () => {
  test('should return schema object', () => {
    const schema = object({ key: string(), children: array(Recur) });
    expectTypeOf(recursive(schema)).toEqualTypeOf<
      SchemaWithRecursive<typeof schema>
    >();
  });

  describe('should infer correct types', () => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema = recursive(
      object({
        value: pipe(string(), transform(Number)),
        children: array(Recur),
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
        ObjectIssue | StringIssue | ArrayIssue
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
    const schema = recursive(
      object({
        record: record(string(), Recur),
        map: map(string(), Recur),
        set: set(Recur),
      })
    );
    interface Output {
      record: { [key: string]: Output };
      map: Map<string, Output>;
      set: Set<Output>;
    }
    expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Output>();
    expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Output>();
  });

  test('should infer recursive values of wrapped schemas', () => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema = recursive(
      object({
        optional: optional(Recur),
        default: optional(Recur, () => ({}) as never),
        nullable: nullable(Recur),
        lazy: lazy(() => Recur),
        tuple: tuple([string(), Recur]),
        tupleWithRest: tupleWithRest([number()], Recur),
        readonly: pipe(array(Recur), readonly()),
        variant: variant('type', [
          object({ type: literal('node'), next: Recur }),
          object({ type: literal('leaf') }),
        ]),
      })
    );
    interface Input {
      optional?: Input | undefined;
      default?: Input | undefined;
      nullable: Input | null;
      lazy: Input;
      tuple: [string, Input];
      tupleWithRest: [number, ...Input[]];
      readonly: Input[];
      variant: { type: 'node'; next: Input } | { type: 'leaf' };
    }
    interface Output {
      optional?: Output | undefined;
      default: Output;
      nullable: Output | null;
      lazy: Output;
      tuple: [string, Output];
      tupleWithRest: [number, ...Output[]];
      readonly readonly: readonly Output[];
      variant: { type: 'node'; next: Output } | { type: 'leaf' };
    }
    expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Input>();
    expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Output>();
  });

  test('should infer recursive types at top level', () => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema1 = recursive(array(Recur));
    type Output1 = Output1[];
    expectTypeOf<InferOutput<typeof schema1>>().toEqualTypeOf<Output1>();

    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema2 = recursive(union([number(), array(Recur)]));
    type Output2 = number | Output2[];
    expectTypeOf<InferOutput<typeof schema2>>().toEqualTypeOf<Output2>();
    expectTypeOf<InferIssue<typeof schema2>>().toEqualTypeOf<
      UnionIssue<NumberIssue | ArrayIssue> | NumberIssue | ArrayIssue
    >();

    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema3 = recursive(record(string(), Recur));
    interface Output3 {
      [key: string]: Output3;
    }
    expectTypeOf<InferOutput<typeof schema3>>().toEqualTypeOf<Output3>();
  });

  describe('should compose with pipe', () => {
    test('for recursive schema inside of pipe', () => {
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      const schema = pipe(
        recursive(object({ value: number(), children: array(Recur) })),
        transform((input) => {
          expectTypeOf(input.children[0].children[0].value).toBeNumber();
          return input.children.length;
        })
      );
      interface Input {
        value: number;
        children: Input[];
      }
      expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Input>();
      expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<number>();
    });

    test('for pipe inside of recursive schema', () => {
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      const schema = recursive(
        pipe(
          object({ value: number(), children: array(Recur) }),
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
  });

  test('should compose with intersect', () => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema = recursive(
      intersect([
        pipe(
          object({ id: string() }),
          transform((input) => ({ key: input.id }))
        ),
        object({ children: optional(array(Recur)) }),
      ])
    );
    interface Input {
      id: string;
      children?: Input[] | undefined;
    }
    interface Output {
      key: string;
      children?: Output[] | undefined;
    }
    expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<Input>();
    expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Output>();
  });

  test('should resolve placeholders to closest recursive schema', () => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema = recursive(
      object({
        tag: recursive(object({ name: string(), parent: optional(Recur) })),
        children: array(Recur),
      })
    );
    interface Tag {
      name: string;
      parent?: Tag | undefined;
    }
    interface Output {
      tag: Tag;
      children: Output[];
    }
    expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Output>();
  });

  test('should keep types without placeholders unchanged', () => {
    class Counter {
      #count = 0;
      get count() {
        return this.#count;
      }
    }
    type Json = string | number | Json[] | { [key: string]: Json };
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema = recursive(
      object({
        any: any(),
        brand: pipe(string(), brand('id')),
        custom: custom<Json>(() => true),
        date: date(),
        instance: instance(Counter),
        intersect: intersect([
          object({ key1: string() }),
          object({ key2: number() }),
        ]),
        children: array(Recur),
      })
    );
    interface Output {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      any: any;
      brand: string & Brand<'id'>;
      custom: Json;
      date: Date;
      instance: Counter;
      intersect: { key1: string } & { key2: number };
      children: Output[];
    }
    expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<Output>();
  });

  test('should not change schemas without placeholders', () => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema = recursive(union([string(), null_()]));
    expectTypeOf<InferInput<typeof schema>>().toEqualTypeOf<string | null>();
    expectTypeOf<InferOutput<typeof schema>>().toEqualTypeOf<string | null>();
  });
});

describe('ResolveRecur', () => {
  test('should replace placeholders with root type', () => {
    interface Root {
      value: string;
      children: RecurMarker[];
    }
    interface Output {
      value: string;
      children: Output[];
    }
    expectTypeOf<ResolveRecur<Root>>().toEqualTypeOf<Output>();
  });

  test('should keep types without placeholders unchanged', () => {
    expectTypeOf<ResolveRecur<string>>().toEqualTypeOf<string>();
    expectTypeOf<ResolveRecur<unknown>>().toEqualTypeOf<unknown>();
    expectTypeOf<ResolveRecur<never>>().toEqualTypeOf<never>();
    expectTypeOf<ResolveRecur<Date>>().toEqualTypeOf<Date>();
    expectTypeOf<ResolveRecur<{ key: string[] }>>().toEqualTypeOf<{
      key: string[];
    }>();
  });
});

describe('HasRecur', () => {
  test('should detect placeholders', () => {
    expectTypeOf<HasRecur<RecurMarker>>().toEqualTypeOf<true>();
    expectTypeOf<HasRecur<RecurMarker | undefined>>().toEqualTypeOf<true>();
    expectTypeOf<HasRecur<{ key: RecurMarker[] }>>().toEqualTypeOf<true>();
    expectTypeOf<HasRecur<Map<string, RecurMarker>>>().toEqualTypeOf<true>();
    expectTypeOf<HasRecur<Set<RecurMarker>>>().toEqualTypeOf<true>();
    expectTypeOf<
      HasRecur<{ key?: readonly [string, RecurMarker] }>
    >().toEqualTypeOf<true>();
    expectTypeOf<
      HasRecur<{ key: string } & RecurMarker>
    >().toEqualTypeOf<true>();
  });

  test('should not detect placeholders', () => {
    interface Tree {
      value: string;
      children: Tree[];
    }
    expectTypeOf<HasRecur<string>>().toEqualTypeOf<false>();
    expectTypeOf<HasRecur<unknown>>().toEqualTypeOf<false>();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expectTypeOf<HasRecur<any>>().toEqualTypeOf<false>();
    expectTypeOf<HasRecur<never>>().toEqualTypeOf<false>();
    expectTypeOf<HasRecur<Tree>>().toEqualTypeOf<false>();
    expectTypeOf<HasRecur<Map<string, Tree[]>>>().toEqualTypeOf<false>();
  });
});

describe('StripRecur', () => {
  test('should replace placeholders with never', () => {
    expectTypeOf<
      StripRecur<{ key: RecurMarker[]; other?: RecurMarker | undefined }>
    >().toEqualTypeOf<{ key: never[]; other?: undefined }>();
  });
});

describe('NoUnresolvedRecur', () => {
  test('should allow resolved schemas', () => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema = recursive(object({ children: array(Recur) }));
    expectTypeOf<NoUnresolvedRecur<typeof schema>>().toEqualTypeOf<unknown>();
    expectTypeOf<
      NoUnresolvedRecur<ReturnType<typeof string>>
    >().toEqualTypeOf<unknown>();
  });

  test('should reject unresolved schemas', () => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema1 = object({ children: array(Recur) });
    expectTypeOf<
      NoUnresolvedRecur<typeof schema1>
    >().toEqualTypeOf<UnresolvedRecur>();

    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema2 = pipe(
      array(Recur),
      transform((input) => input.length)
    );
    expectTypeOf<
      NoUnresolvedRecur<typeof schema2>
    >().toEqualTypeOf<UnresolvedRecur>();

    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const schema3 = pipe(
      string(),
      transform(() => ({}) as RecurMarker)
    );
    expectTypeOf<
      NoUnresolvedRecur<typeof schema3>
    >().toEqualTypeOf<UnresolvedRecur>();
  });
});

import { describe, expectTypeOf, test } from 'vitest';
import { transformAsync } from '../../actions/index.ts';
import {
  arrayAsync,
  number,
  objectAsync,
  optionalAsync,
  recordAsync,
  string,
} from '../../schemas/index.ts';
import type { InferInput, InferOutput } from '../../types/index.ts';
import { parseAsync } from '../parse/index.ts';
import { pipeAsync } from '../pipe/index.ts';
import { safeParseAsync, type SafeParseResult } from '../safeParse/index.ts';
import { Recur } from './recur.ts';
import { recursiveAsync } from './recursiveAsync.ts';

describe('recursiveAsync', () => {
  test('should infer async recursion and transformed output', async () => {
    const schema = recursiveAsync(
      pipeAsync(
        objectAsync({
          value: string(),
          children: arrayAsync(Recur),
        }),
        transformAsync(async (input) => ({
          ...input,
          total: input.children.length,
        }))
      )
    );
    type Input = InferInput<typeof schema>;
    type Output = InferOutput<typeof schema>;
    expectTypeOf<Input>().toEqualTypeOf<{
      value: string;
      children: Input[];
    }>();
    expectTypeOf<Output>().toEqualTypeOf<{
      value: string;
      children: Output[];
      total: number;
    }>();
    expectTypeOf(
      await parseAsync(schema, { value: 'a', children: [] })
    ).toEqualTypeOf<Output>();
    expectTypeOf(
      await safeParseAsync(schema, { value: 'a', children: [] })
    ).toEqualTypeOf<SafeParseResult<typeof schema>>();
  });

  test('should infer record value recursion', () => {
    const schema = recursiveAsync(
      recordAsync(
        string(),
        objectAsync({ id: number(), child: optionalAsync(Recur) })
      )
    );
    expectTypeOf(schema.type).toEqualTypeOf<'recursive'>();
    type Input = InferInput<typeof schema>;
    type Output = InferOutput<typeof schema>;
    expectTypeOf<Input>().toEqualTypeOf<{
      [key: string]: { id: number; child?: Input | undefined };
    }>();
    expectTypeOf<Output>().toEqualTypeOf<{
      [key: string]: { id: number; child?: Output | undefined };
    }>();
  });

  test('should reject unresolved Recur in async parse functions', () => {
    const unresolved = objectAsync({ child: Recur });
    // @ts-expect-error
    parseAsync(unresolved, { child: {} });
    // @ts-expect-error
    safeParseAsync(unresolved, { child: {} });

    const inputOnly = pipeAsync(
      objectAsync({ child: arrayAsync(Recur) }),
      transformAsync(async () => 'ok' as const)
    );
    // @ts-expect-error
    parseAsync(inputOnly, { child: [] });

    const outputOnly = pipeAsync(
      string(),
      transformAsync(
        async (): Promise<{ child: InferOutput<typeof Recur> }> =>
          ({ child: undefined }) as never
      )
    );
    // @ts-expect-error
    safeParseAsync(outputOnly, 'value');

    expectTypeOf(parseAsync(string(), 'ok')).toEqualTypeOf<Promise<string>>();
  });
});

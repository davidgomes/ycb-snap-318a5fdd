import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  InferInput,
  InferIssue,
  InferOutput,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import type { recursive } from './recursive.ts';
import { getRecurStack } from './stack.ts';
import type { ResolveRecur } from './types.ts';

/**
 * Recursive schema async interface.
 */
export interface RecursiveSchemaAsync<
  TWrapped extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> extends BaseSchemaAsync<
    ResolveRecur<InferInput<TWrapped>>,
    ResolveRecur<InferOutput<TWrapped>>,
    InferIssue<TWrapped>
  > {
  /**
   * The schema type.
   */
  readonly type: 'recursive';
  /**
   * The schema reference.
   */
  readonly reference: typeof recursive | typeof recursiveAsync;
  /**
   * The expected property.
   */
  readonly expects: TWrapped['expects'];
  /**
   * The wrapped schema.
   */
  readonly wrapped: TWrapped;
}

/**
 * Resolves `Recur` placeholders in an async schema so it can reference itself.
 *
 * @param schema The schema that contains `Recur`.
 *
 * @returns An async recursive schema.
 */
export function recursiveAsync<
  const TWrapped extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
>(schema: TWrapped): RecursiveSchemaAsync<TWrapped>;

// @__NO_SIDE_EFFECTS__
export function recursiveAsync(
  schema:
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>
): BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>> {
  return {
    kind: 'schema',
    type: 'recursive',
    reference: recursiveAsync,
    expects: schema.expects,
    async: true,
    wrapped: schema,
    get '~standard'() {
      return _getStandardProps(this);
    },
    async '~run'(dataset, config) {
      const stack = getRecurStack(config);
      stack.push(this);
      try {
        return await schema['~run'](dataset, config);
      } finally {
        stack.pop();
      }
    },
  } as BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>;
}

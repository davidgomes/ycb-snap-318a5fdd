import type {
  BaseIssue,
  BaseSchema,
  InferInput,
  InferIssue,
  InferOutput,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import { getRecurStack } from './stack.ts';
import type { ResolveRecur } from './types.ts';

/**
 * Recursive schema interface.
 */
export interface RecursiveSchema<
  TWrapped extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
> extends BaseSchema<
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
  readonly reference: typeof recursive;
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
 * Resolves `Recur` placeholders in a sync schema so it can reference itself.
 *
 * @param schema The schema that contains `Recur`.
 *
 * @returns A recursive schema.
 */
export function recursive<
  const TWrapped extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
>(schema: TWrapped): RecursiveSchema<TWrapped>;

// @__NO_SIDE_EFFECTS__
export function recursive(
  schema: BaseSchema<unknown, unknown, BaseIssue<unknown>>
): BaseSchema<unknown, unknown, BaseIssue<unknown>> {
  return {
    kind: 'schema',
    type: 'recursive',
    reference: recursive,
    expects: schema.expects,
    async: false,
    wrapped: schema,
    get '~standard'() {
      return _getStandardProps(this);
    },
    '~run'(dataset, config) {
      const stack = getRecurStack(config);
      stack.push(this);
      try {
        return schema['~run'](dataset, config);
      } finally {
        stack.pop();
      }
    },
  } as BaseSchema<unknown, unknown, BaseIssue<unknown>>;
}

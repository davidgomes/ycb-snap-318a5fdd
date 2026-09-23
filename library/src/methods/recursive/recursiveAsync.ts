import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  InferIssue,
  OutputDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import type { recursive } from './recursive.ts';
import type {
  InferRecursiveInput,
  InferRecursiveOutput,
  RecursiveConfig,
} from './types.ts';

/**
 * Recursive schema async interface.
 */
export interface RecursiveSchemaAsync<
  TWrapped extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> extends BaseSchemaAsync<
    InferRecursiveInput<TWrapped>,
    InferRecursiveOutput<TWrapped>,
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
 * Creates a recursive schema that resolves the `Recur` placeholders of the
 * wrapped schema to the wrapped schema itself.
 *
 * Hint: If the wrapped schema is async, every schema between it and a `Recur`
 * placeholder must be async too.
 *
 * @param wrapped The wrapped schema.
 *
 * @returns A recursive schema.
 */
// @__NO_SIDE_EFFECTS__
export function recursiveAsync<
  const TWrapped extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
>(wrapped: TWrapped): RecursiveSchemaAsync<TWrapped> {
  return {
    kind: 'schema',
    type: 'recursive',
    reference: recursiveAsync,
    expects: wrapped.expects,
    async: true,
    wrapped,
    get '~standard'() {
      return _getStandardProps(this);
    },
    async '~run'(dataset, config) {
      return this.wrapped['~run'](dataset, {
        ...config,
        '~recur': this.wrapped,
      } as RecursiveConfig) as OutputDataset<
        InferRecursiveOutput<TWrapped>,
        InferIssue<TWrapped>
      >;
    },
  };
}

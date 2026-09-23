import type {
  BaseIssue,
  BaseSchema,
  InferIssue,
  OutputDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import type {
  InferRecursiveInput,
  InferRecursiveOutput,
  RecursiveConfig,
} from './types.ts';

/**
 * Recursive schema interface.
 */
export interface RecursiveSchema<
  TWrapped extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
> extends BaseSchema<
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
 * Creates a recursive schema that resolves the `Recur` placeholders of the
 * wrapped schema to the wrapped schema itself.
 *
 * @param wrapped The wrapped schema.
 *
 * @returns A recursive schema.
 */
// @__NO_SIDE_EFFECTS__
export function recursive<
  const TWrapped extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
>(wrapped: TWrapped): RecursiveSchema<TWrapped> {
  return {
    kind: 'schema',
    type: 'recursive',
    reference: recursive,
    expects: wrapped.expects,
    async: false,
    wrapped,
    get '~standard'() {
      return _getStandardProps(this);
    },
    '~run'(dataset, config) {
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

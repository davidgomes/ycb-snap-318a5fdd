import type {
  BaseIssue,
  BaseSchema,
  InferIssue,
  OutputDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import { _withRecurTarget } from './bindRecur.ts';
import type { RecursiveInput, RecursiveOutput } from './types.ts';

/**
 * Recursive schema interface.
 */
export interface RecursiveSchema<
  TSchema extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
> extends BaseSchema<
    RecursiveInput<TSchema>,
    RecursiveOutput<TSchema>,
    InferIssue<TSchema>
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
  readonly expects: TSchema['expects'];
  /**
   * The wrapped schema.
   */
  readonly wrapped: TSchema;
}

/**
 * Resolves `Recur` placeholders in a sync schema.
 *
 * @param schema The schema that contains `Recur`.
 *
 * @returns A sync schema with `Recur` bound to itself.
 */
// @__NO_SIDE_EFFECTS__
export function recursive<
  const TSchema extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
>(schema: TSchema): RecursiveSchema<TSchema> {
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
      // Forward parse to wrapped schema with this schema bound to Recur
      return this.wrapped['~run'](
        dataset,
        _withRecurTarget(config, this)
      ) as OutputDataset<RecursiveOutput<TSchema>, InferIssue<TSchema>>;
    },
  };
}

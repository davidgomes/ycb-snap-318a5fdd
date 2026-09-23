import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  InferIssue,
  OutputDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import { _withRecurTarget } from './bindRecur.ts';
import type { recursive } from './recursive.ts';
import type { RecursiveInput, RecursiveOutput } from './types.ts';

/**
 * Recursive schema async interface.
 */
export interface RecursiveSchemaAsync<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> extends BaseSchemaAsync<
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
  readonly reference: typeof recursive | typeof recursiveAsync;
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
 * Resolves `Recur` placeholders in a sync or async schema.
 *
 * @param schema The schema that contains `Recur`.
 *
 * @returns An async schema with `Recur` bound to itself.
 */
// @__NO_SIDE_EFFECTS__
export function recursiveAsync<
  const TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
>(schema: TSchema): RecursiveSchemaAsync<TSchema> {
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
      // Forward parse to wrapped schema with this schema bound to Recur
      return (await this.wrapped['~run'](
        dataset,
        _withRecurTarget(config, this)
      )) as OutputDataset<RecursiveOutput<TSchema>, InferIssue<TSchema>>;
    },
  };
}

import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  InferIssue,
  OutputDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import { pushRecursiveSchema } from './stack.ts';
import type { RecursiveInput, RecursiveOutput } from './types.ts';

/**
 * Recursive async schema interface.
 */
export interface RecursiveSchemaAsync<
  TWrapped extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> extends BaseSchemaAsync<
    RecursiveInput<TWrapped>,
    RecursiveOutput<TWrapped>,
    InferIssue<TWrapped>
  > {
  /**
   * The schema type.
   */
  readonly type: 'recursive';
  /**
   * The schema reference.
   */
  readonly reference: typeof recursiveAsync;
  /**
   * The expected property.
   */
  readonly expects: 'unknown';
  /**
   * The wrapped schema.
   */
  readonly wrapped: TWrapped;
}

/**
 * Resolves `Recur` placeholders inside a sync or async schema.
 *
 * Hint: `Recur` refers to the schema passed to this function. Use async
 * container schemas when a nested value is validated asynchronously.
 *
 * @param schema The schema that contains `Recur`.
 *
 * @returns The schema with self-references resolved.
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
    expects: 'unknown',
    async: true,
    wrapped: schema,
    get '~standard'() {
      return _getStandardProps(this);
    },
    async '~run'(dataset, config) {
      return schema['~run'](
        dataset,
        pushRecursiveSchema(config, schema)
      ) as OutputDataset<RecursiveOutput<TSchema>, InferIssue<TSchema>>;
    },
  };
}

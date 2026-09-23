import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  InferIssue,
  OutputDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import { _bindRecur } from './context.ts';
import type { InferRecursiveInput, InferRecursiveOutput } from './types.ts';

/**
 * Recursive schema async interface.
 */
export interface RecursiveSchemaAsync<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> extends BaseSchemaAsync<
    InferRecursiveInput<TSchema>,
    InferRecursiveOutput<TSchema>,
    InferIssue<TSchema>
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
  readonly expects: TSchema['expects'];
  /**
   * The wrapped schema.
   */
  readonly wrapped: TSchema;
}

/**
 * Resolves `Recur` placeholders in an async schema.
 *
 * Hint: Place `Recur` where the schema should refer to itself, then pass the
 * finished schema to `recursiveAsync`. Nested containers must be async so they
 * can await the recursive parse.
 *
 * @param schema The schema to resolve.
 *
 * @returns A recursive schema.
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
      // Bind this schema and parse wrapped schema
      return schema['~run'](dataset, _bindRecur(config, this)) as
        | OutputDataset<InferRecursiveOutput<TSchema>, InferIssue<TSchema>>
        | Promise<
            OutputDataset<InferRecursiveOutput<TSchema>, InferIssue<TSchema>>
          >;
    },
  };
}

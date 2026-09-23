import type {
  BaseIssue,
  BaseSchema,
  InferIssue,
  OutputDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import { pushRecursiveSchema } from './stack.ts';
import type { RecursiveInput, RecursiveOutput } from './types.ts';

/**
 * Recursive schema interface.
 */
export interface RecursiveSchema<
  TWrapped extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
> extends BaseSchema<
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
  readonly reference: typeof recursive;
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
 * Resolves `Recur` placeholders inside a sync schema.
 *
 * Hint: `Recur` refers to the schema passed to this function. Nested
 * `recursive` and `recursiveAsync` calls keep their own self-reference.
 *
 * @param schema The schema that contains `Recur`.
 *
 * @returns The schema with self-references resolved.
 */
// @__NO_SIDE_EFFECTS__
export function recursive<
  const TSchema extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
>(schema: TSchema): RecursiveSchema<TSchema> {
  return {
    kind: 'schema',
    type: 'recursive',
    reference: recursive,
    expects: 'unknown',
    async: false,
    wrapped: schema,
    get '~standard'() {
      return _getStandardProps(this);
    },
    '~run'(dataset, config) {
      return schema['~run'](
        dataset,
        pushRecursiveSchema(config, schema)
      ) as OutputDataset<RecursiveOutput<TSchema>, InferIssue<TSchema>>;
    },
  };
}

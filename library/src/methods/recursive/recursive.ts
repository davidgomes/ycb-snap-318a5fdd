import type {
  BaseIssue,
  BaseSchema,
  InferIssue,
  OutputDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import { _bindRecur } from './context.ts';
import type { InferRecursiveInput, InferRecursiveOutput } from './types.ts';

/**
 * Recursive schema interface.
 */
export interface RecursiveSchema<
  TSchema extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
> extends BaseSchema<
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
 * Hint: Place `Recur` where the schema should refer to itself, then pass the
 * finished schema to `recursive`. Use `recursiveAsync` for async schemas.
 *
 * @param schema The schema to resolve.
 *
 * @returns A recursive schema.
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
      // Bind this schema and parse wrapped schema
      return schema['~run'](dataset, _bindRecur(config, this)) as OutputDataset<
        InferRecursiveOutput<TSchema>,
        InferIssue<TSchema>
      >;
    },
  };
}

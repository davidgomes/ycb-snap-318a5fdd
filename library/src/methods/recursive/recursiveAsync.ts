import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  Config,
  InferIssue,
  OutputDataset,
  StandardProps,
  UnknownDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import type { InferRecursiveInput, InferRecursiveOutput } from './types.ts';

/**
 * Schema with recursive async type.
 */
export type SchemaWithRecursiveAsync<
  TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> = Omit<TSchema, 'async' | '~standard' | '~run' | '~types'> & {
  /**
   * Whether it's async.
   */
  readonly async: true;
  /**
   * The Standard Schema properties.
   *
   * @internal
   */
  readonly '~standard': StandardProps<
    InferRecursiveInput<TSchema>,
    InferRecursiveOutput<TSchema>
  >;
  /**
   * Parses unknown input values.
   *
   * @param dataset The input dataset.
   * @param config The configuration.
   *
   * @returns The output dataset.
   *
   * @internal
   */
  readonly '~run': (
    dataset: UnknownDataset,
    config: Config<BaseIssue<unknown>>
  ) => Promise<
    OutputDataset<InferRecursiveOutput<TSchema>, InferIssue<TSchema>>
  >;
  /**
   * The input, output and issue type.
   *
   * @internal
   */
  readonly '~types'?:
    | {
        readonly input: InferRecursiveInput<TSchema>;
        readonly output: InferRecursiveOutput<TSchema>;
        readonly issue: InferIssue<TSchema>;
      }
    | undefined;
};

/**
 * Resolves the `Recur` placeholders of a schema by referencing the schema
 * itself.
 *
 * @param schema The schema to resolve.
 *
 * @returns A recursive schema.
 */
export function recursiveAsync<
  const TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
>(schema: TSchema): SchemaWithRecursiveAsync<TSchema>;

// @__NO_SIDE_EFFECTS__
export function recursiveAsync(
  schema:
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>
): SchemaWithRecursiveAsync<
  | BaseSchema<unknown, unknown, BaseIssue<unknown>>
  | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>
> {
  return {
    ...schema,
    async: true,
    get '~standard'() {
      return _getStandardProps(this);
    },
    async '~run'(dataset, config) {
      // Parse input with schema and bind schema to `Recur` placeholders
      return schema['~run'](dataset, {
        ...config,
        '~recur': schema,
      } as Config<BaseIssue<unknown>>);
    },
  };
}

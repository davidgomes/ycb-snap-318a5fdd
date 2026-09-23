import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  Config,
  InferInput,
  InferIssue,
  InferOutput,
  OutputDataset,
  StandardProps,
  UnknownDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import type { RecurConfig, ResolveRecur } from './types.ts';

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
    ResolveRecur<InferInput<TSchema>>,
    ResolveRecur<InferOutput<TSchema>>
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
    OutputDataset<ResolveRecur<InferOutput<TSchema>>, InferIssue<TSchema>>
  >;
  /**
   * The input, output and issue type.
   *
   * @internal
   */
  readonly '~types'?:
    | {
        readonly input: ResolveRecur<InferInput<TSchema>>;
        readonly output: ResolveRecur<InferOutput<TSchema>>;
        readonly issue: InferIssue<TSchema>;
      }
    | undefined;
};

/**
 * Resolves the `Recur` placeholders of a schema, so that they refer to the
 * schema itself.
 *
 * Hint: If the schema is async, place `Recur` inside of async schemas like
 * `arrayAsync`, because only they await the recursively parsed values.
 *
 * @param schema The schema to resolve.
 *
 * @returns The recursive schema.
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
      // Hint: The schema is passed down with the configuration, so that
      // nested placeholders can refer to it without mutating any state
      const recurConfig: RecurConfig = { ...config, '~recur': schema };
      return schema['~run'](dataset, recurConfig);
    },
  };
}

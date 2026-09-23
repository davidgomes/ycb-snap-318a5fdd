import type {
  BaseIssue,
  BaseSchema,
  Config,
  InferIssue,
  OutputDataset,
  StandardProps,
  UnknownDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import type { InferRecursiveInput, InferRecursiveOutput } from './types.ts';

/**
 * Schema with recursive type.
 */
export type SchemaWithRecursive<
  TSchema extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
> = Omit<TSchema, '~standard' | '~run' | '~types'> & {
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
  ) => OutputDataset<InferRecursiveOutput<TSchema>, InferIssue<TSchema>>;
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
export function recursive<
  const TSchema extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
>(schema: TSchema): SchemaWithRecursive<TSchema>;

// @__NO_SIDE_EFFECTS__
export function recursive(
  schema: BaseSchema<unknown, unknown, BaseIssue<unknown>>
): SchemaWithRecursive<BaseSchema<unknown, unknown, BaseIssue<unknown>>> {
  return {
    ...schema,
    get '~standard'() {
      return _getStandardProps(this);
    },
    '~run'(dataset, config) {
      // Parse input with schema and bind schema to `Recur` placeholders
      return schema['~run'](dataset, {
        ...config,
        '~recur': schema,
      } as Config<BaseIssue<unknown>>);
    },
  };
}

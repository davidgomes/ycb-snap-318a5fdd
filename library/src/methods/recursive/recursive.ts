import type {
  BaseIssue,
  BaseSchema,
  Config,
  InferInput,
  InferIssue,
  InferOutput,
  OutputDataset,
  StandardProps,
  UnknownDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import type { RecurConfig, RecurMarker, ResolveRecur } from './types.ts';

/**
 * Recur schema interface.
 */
export interface RecurSchema
  extends BaseSchema<RecurMarker, RecurMarker, never> {
  /**
   * The schema type.
   */
  readonly type: 'recur';
  /**
   * The expected property.
   */
  readonly expects: 'unknown';
}

/**
 * Recur placeholder that refers to the closest enclosing schema wrapped with
 * `recursive` or `recursiveAsync`.
 *
 * Hint: Schemas containing `Recur` must be wrapped with `recursive` or
 * `recursiveAsync` before they can be parsed. Until then, the placeholder is
 * typed as `RecurMarker`, so transformations inside of the wrapped schema can
 * pass recursive values through, but not access their properties.
 */
export const Recur: RecurSchema = {
  kind: 'schema',
  type: 'recur',
  reference: recursive as RecurSchema['reference'],
  expects: 'unknown',
  async: false,
  get '~standard'() {
    return _getStandardProps(this);
  },
  '~run'(dataset, config) {
    // Get schema that placeholder refers to from configuration
    const schema = (config as RecurConfig)['~recur'];

    // If placeholder is not resolved, throw error
    if (!schema) {
      throw new Error(
        'Recur must be resolved with recursive or recursiveAsync before parsing.'
      );
    }

    // Hint: Inside of `recursiveAsync`, the dataset can be a promise that is
    // awaited by the enclosing async schema
    return schema['~run'](dataset, config) as OutputDataset<RecurMarker, never>;
  },
};

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
  ) => OutputDataset<ResolveRecur<InferOutput<TSchema>>, InferIssue<TSchema>>;
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
 * @param schema The schema to resolve.
 *
 * @returns The recursive schema.
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
      // Hint: The schema is passed down with the configuration, so that
      // nested placeholders can refer to it without mutating any state
      const recurConfig: RecurConfig = { ...config, '~recur': schema };
      return schema['~run'](dataset, recurConfig);
    },
  };
}

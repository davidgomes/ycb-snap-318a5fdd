import { getGlobalConfig } from '../../storages/index.ts';
import type {
  BaseIssue,
  BaseSchema,
  Config,
  InferIssue,
  InferOutput,
} from '../../types/index.ts';
import { ValiError } from '../../utils/index.ts';
import type { IsUnresolvedRecur } from '../recursive/types.ts';

/**
 * Parses an unknown input based on a schema.
 *
 * @param schema The schema to be used.
 * @param input The input to be parsed.
 * @param config The parse configuration.
 *
 * @returns The parsed input.
 */
export function parse<
  const TSchema extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
>(
  schema: TSchema & (IsUnresolvedRecur<TSchema> extends true ? never : unknown),
  input: unknown,
  config?: Config<InferIssue<TSchema>>
): InferOutput<TSchema>;

export function parse(
  schema: BaseSchema<unknown, unknown, BaseIssue<unknown>>,
  input: unknown,
  config?: Config<BaseIssue<unknown>>
): unknown {
  const dataset = schema['~run']({ value: input }, getGlobalConfig(config));
  if (dataset.issues) {
    throw new ValiError(dataset.issues);
  }
  return dataset.value;
}

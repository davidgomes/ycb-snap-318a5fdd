import { getGlobalConfig } from '../../storages/index.ts';
import type {
  BaseIssue,
  BaseSchema,
  Config,
  InferIssue,
  InferOutput,
} from '../../types/index.ts';
import { ValiError } from '../../utils/index.ts';
import type { RecurResolved } from '../recursive/types.ts';

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
  schema: TSchema & RecurResolved<TSchema>,
  input: unknown,
  config?: Config<InferIssue<TSchema>>
): InferOutput<TSchema> {
  const dataset = schema['~run']({ value: input }, getGlobalConfig(config));
  if (dataset.issues) {
    throw new ValiError(dataset.issues);
  }
  return dataset.value;
}

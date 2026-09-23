import { getGlobalConfig } from '../../storages/index.ts';
import type {
  BaseIssue,
  BaseSchema,
  Config,
  InferIssue,
} from '../../types/index.ts';
import type { EnsureRecurResolved } from '../recursive/types.ts';
import type { SafeParseResult } from './types.ts';

/**
 * Parses an unknown input based on a schema.
 *
 * @param schema The schema to be used.
 * @param input The input to be parsed.
 * @param config The parse configuration.
 *
 * @returns The parse result.
 */
export function safeParse<
  const TSchema extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
>(
  schema: EnsureRecurResolved<TSchema>,
  input: unknown,
  config?: Config<InferIssue<TSchema>>
): SafeParseResult<TSchema>;

// @__NO_SIDE_EFFECTS__
export function safeParse(
  schema: BaseSchema<unknown, unknown, BaseIssue<unknown>>,
  input: unknown,
  config?: Config<BaseIssue<unknown>>
): SafeParseResult<BaseSchema<unknown, unknown, BaseIssue<unknown>>> {
  const dataset = schema['~run']({ value: input }, getGlobalConfig(config));
  return {
    typed: dataset.typed,
    success: !dataset.issues,
    output: dataset.value,
    issues: dataset.issues,
  } as SafeParseResult<BaseSchema<unknown, unknown, BaseIssue<unknown>>>;
}

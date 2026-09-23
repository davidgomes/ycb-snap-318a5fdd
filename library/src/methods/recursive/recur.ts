import type { BaseSchema, SuccessDataset } from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import { readRecur } from './stack.ts';
import type { RecurMark } from './types.ts';

/**
 * Reference used to identify the `Recur` placeholder.
 *
 * @returns The `Recur` placeholder schema.
 */
function recur(): RecurSchema {
  return Recur;
}

/**
 * Recur placeholder schema interface.
 */
export interface RecurSchema extends BaseSchema<RecurMark, RecurMark, never> {
  /**
   * The schema type.
   */
  readonly type: 'recur';
  /**
   * The schema reference.
   */
  readonly reference: typeof recur;
  /**
   * The expected property.
   */
  readonly expects: 'unknown';
}

/**
 * Placeholder schema for a recursive reference.
 *
 * Hint: Place `Recur` directly inside a composed schema, then wrap the
 * finished schema with `recursive` or `recursiveAsync` to resolve those self
 * references.
 */
export const Recur: RecurSchema = {
  kind: 'schema',
  type: 'recur',
  reference: recur,
  expects: 'unknown',
  async: false,
  get '~standard'() {
    return _getStandardProps(this);
  },
  '~run'(dataset, config) {
    const schema = readRecur(config);
    if (!schema) {
      throw new Error(
        'Unresolved Recur placeholder: wrap the schema with recursive or recursiveAsync before parsing.'
      );
    }
    return schema['~run'](dataset, config) as SuccessDataset<RecurMark>;
  },
};

import type { BaseSchema, OutputDataset } from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import { _getRecurTarget } from './bindRecur.ts';
import type { RecurMark } from './types.ts';

/**
 * Recur schema interface.
 */
export interface RecurSchema extends BaseSchema<RecurMark, RecurMark, never> {
  /**
   * The schema type.
   */
  readonly type: 'recur';
  /**
   * The schema reference.
   */
  readonly reference: () => RecurSchema;
  /**
   * The expected property.
   */
  readonly expects: 'unknown';
}

/**
 * Returns the `Recur` placeholder schema.
 *
 * @returns The `Recur` placeholder schema.
 */
function recurReference(): RecurSchema {
  return Recur;
}

/**
 * Placeholder schema for a recursive reference.
 *
 * Place `Recur` where a schema should refer to itself, then wrap the finished
 * schema with `recursive` or `recursiveAsync`.
 */
export const Recur: RecurSchema = {
  kind: 'schema',
  type: 'recur',
  reference: recurReference,
  expects: 'unknown',
  async: false,
  get '~standard'() {
    return _getStandardProps(this);
  },
  '~run'(dataset, config) {
    // Get schema bound by the enclosing recursive wrapper
    const schema = _getRecurTarget(config);

    // If placeholder was not wrapped, throw developer error
    if (!schema) {
      throw new Error(
        'Unresolved Recur placeholder. Wrap the schema with recursive or recursiveAsync.'
      );
    }

    // Parse nested value with the bound recursive schema
    return schema['~run'](dataset, config) as OutputDataset<RecurMark, never>;
  },
};

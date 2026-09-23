import type { BaseSchema, OutputDataset } from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import { recursive } from './recursive.ts';
import type { RecurPlaceholder, RecursiveConfig } from './types.ts';

/**
 * Recur schema interface.
 */
export interface RecurSchema
  extends BaseSchema<RecurPlaceholder, RecurPlaceholder, never> {
  /**
   * The schema type.
   */
  readonly type: 'recur';
  /**
   * The schema reference.
   */
  readonly reference: typeof recursive;
  /**
   * The expected property.
   */
  readonly expects: 'unknown';
}

/**
 * Recur placeholder schema.
 *
 * Hint: It references the schema wrapped by the closest enclosing `recursive`
 * or `recursiveAsync`. If that schema is async, every schema between it and
 * the placeholder must be async too.
 */
export const Recur: RecurSchema = {
  kind: 'schema',
  type: 'recur',
  reference: recursive,
  expects: 'unknown',
  async: false,
  get '~standard'() {
    return _getStandardProps(this);
  },
  '~run'(dataset, config) {
    // Get wrapped schema of closest recursive schema
    const schema = (config as RecursiveConfig)['~recur'];

    // If placeholder is unresolved, throw error
    if (!schema) {
      throw new Error(
        'Unresolved Recur placeholder. Wrap the schema with recursive(...) or recursiveAsync(...) first.'
      );
    }

    // Otherwise, parse input with wrapped schema
    return schema['~run'](dataset, config) as OutputDataset<
      RecurPlaceholder,
      never
    >;
  },
};

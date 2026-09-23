import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  Config,
  OutputDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import type { RecurType } from './types.ts';

/**
 * Recur schema interface.
 */
export interface RecurSchema extends BaseSchema<RecurType, RecurType, never> {
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
 * Recur placeholder schema. It references the schema that is wrapped with
 * the nearest enclosing `recursive` or `recursiveAsync` method.
 *
 * Hint: If the wrapped schema is async, the schemas between it and the
 * placeholder must also be async, because the placeholder then returns a
 * promise.
 */
export const Recur: RecurSchema = {
  kind: 'schema',
  type: 'recur',
  reference: () => Recur,
  expects: 'unknown',
  async: false,
  get '~standard'() {
    return _getStandardProps(this);
  },
  '~run'(dataset, config) {
    // Get schema bound by nearest enclosing recursive wrapper
    const schema = (
      config as Config<BaseIssue<unknown>> & {
        readonly '~recur'?:
          | BaseSchema<unknown, unknown, BaseIssue<unknown>>
          | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>;
      }
    )['~recur'];

    // If placeholder is not resolved, throw error
    if (!schema) {
      throw new Error(
        'Unresolved Recur placeholder. Wrap the schema with recursive(...) or recursiveAsync(...) first.'
      );
    }

    // Parse input with bound schema
    return schema['~run'](dataset, config) as OutputDataset<RecurType, never>;
  },
};

import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  Config,
  OutputDataset,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import type { RecurMarker } from './types.ts';

/**
 * Recur config interface.
 *
 * @internal
 */
export interface RecurConfig extends Config<BaseIssue<unknown>> {
  /**
   * The nearest enclosing recursive schema.
   */
  readonly '~recur'?:
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>;
}

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
   * The schema reference.
   */
  readonly reference: () => RecurSchema;
  /**
   * The expected property.
   */
  readonly expects: 'unknown';
}

/**
 * Recur placeholder that references the nearest enclosing `recursive` or
 * `recursiveAsync` schema.
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
    const schema = (config as RecurConfig)['~recur'];
    if (!schema) {
      throw new Error(
        'Unresolved Recur placeholder. Wrap the schema with recursive or recursiveAsync.'
      );
    }
    // Hint: Inside async schemas, the dataset may be a promise, which async
    // parents await
    return schema['~run'](dataset, config) as OutputDataset<RecurMarker, never>;
  },
};

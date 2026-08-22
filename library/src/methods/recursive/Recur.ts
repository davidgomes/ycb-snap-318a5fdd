import type { FailureDataset, OutputDataset } from '../../types/dataset.ts';
import type { BaseIssue } from '../../types/issue.ts';
import type { BaseSchema } from '../../types/schema.ts';
import { _addIssue, _getStandardProps } from '../../utils/index.ts';
import type { RecurPlaceholder } from './types.ts';
import { _getRecurRoot } from './utils.ts';

/**
 * Recur issue interface.
 */
export interface RecurIssue extends BaseIssue<unknown> {
  /**
   * The issue kind.
   */
  readonly kind: 'schema';
  /**
   * The issue type.
   */
  readonly type: 'recur';
  /**
   * The expected property.
   */
  readonly expected: 'unknown';
}

/**
 * Recur schema interface.
 */
export interface RecurSchema
  extends BaseSchema<RecurPlaceholder, RecurPlaceholder, RecurIssue> {
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
 * Recur placeholder schema.
 *
 * Hint: Place `Recur` inside a composed schema to mark self-references, then
 * wrap the finished schema with `recursive` or `recursiveAsync` to resolve
 * them.
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
    // Get current recur root
    const root = _getRecurRoot(config);

    // If root is missing, add issue
    if (!root) {
      _addIssue(this, 'type', dataset, config);
      // @ts-expect-error
      return dataset as FailureDataset<RecurIssue>;
    }

    // Parse input with wrapped root schema
    return root.wrapped['~run'](dataset, config) as OutputDataset<
      RecurPlaceholder,
      RecurIssue
    >;
  },
};

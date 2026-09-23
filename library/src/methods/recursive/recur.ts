import type {
  BaseIssue,
  BaseSchema,
  FailureDataset,
  OutputDataset,
} from '../../types/index.ts';
import { _addIssue, _getStandardProps } from '../../utils/index.ts';
import { getRecursiveSchema } from './stack.ts';
import type { RecurInput, RecurOutput } from './types.ts';

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
  readonly expected: 'recursive';
}

/**
 * Recur schema interface.
 */
export interface RecurSchema
  extends BaseSchema<RecurInput, RecurOutput, RecurIssue> {
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
  readonly expects: 'recursive';
}

/**
 * Reference function of {@link Recur}.
 *
 * @returns The recur placeholder schema.
 */
function recur(): RecurSchema {
  return Recur;
}

/**
 * Placeholder schema for a self-reference.
 *
 * Place it where the surrounding schema should appear again, then pass the
 * finished schema to `recursive` or `recursiveAsync`.
 */
export const Recur: RecurSchema = {
  kind: 'schema',
  type: 'recur',
  reference: recur,
  expects: 'recursive',
  async: false,
  get '~standard'() {
    return _getStandardProps(this);
  },
  '~run'(dataset, config) {
    const schema = getRecursiveSchema(config);
    if (!schema) {
      _addIssue(this, 'type', dataset, config);
      // @ts-expect-error
      return dataset as FailureDataset<RecurIssue>;
    }
    return schema['~run'](dataset, config) as OutputDataset<
      RecurOutput,
      RecurIssue
    >;
  },
};

import type {
  BaseIssue,
  BaseSchema,
  OutputDataset,
} from '../../types/index.ts';
import { _addIssue, _getStandardProps } from '../../utils/index.ts';
import { _readRecur } from './context.ts';

declare const RecurInputBrand: unique symbol;
declare const RecurOutputBrand: unique symbol;

/**
 * Recur input placeholder interface.
 *
 * Positions that contain this type are replaced with the recursive schema's
 * own input type by `recursive` and `recursiveAsync`.
 */
export interface RecurInput {
  /**
   * The input placeholder brand.
   */
  readonly [RecurInputBrand]: 'input';
}

/**
 * Recur output placeholder interface.
 *
 * Positions that contain this type are replaced with the recursive schema's
 * own output type by `recursive` and `recursiveAsync`.
 */
export interface RecurOutput {
  /**
   * The output placeholder brand.
   */
  readonly [RecurOutputBrand]: 'output';
}

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
  readonly expected: 'Recur';
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
  readonly reference: () => RecurSchema;
  /**
   * The expected property.
   */
  readonly expects: 'Recur';
}

/**
 * Placeholder schema for recursive schema composition.
 *
 * Hint: Place `Recur` where a schema should refer to itself, then wrap the
 * finished schema with `recursive` or `recursiveAsync`.
 */
export const Recur: RecurSchema = {
  kind: 'schema',
  type: 'recur',
  reference() {
    return Recur;
  },
  expects: 'Recur',
  async: false,
  get '~standard'() {
    return _getStandardProps(this);
  },
  '~run'(dataset, config) {
    // Get recursive schema bound to this placeholder
    const target = _readRecur(config);

    // If placeholder is unresolved, add issue
    if (!target) {
      _addIssue(this, 'type', dataset, config);
      // @ts-expect-error
      return dataset as OutputDataset<RecurOutput, RecurIssue>;
    }

    // Hint: Async targets return a promise. Async containers await it, while
    // sync targets return a dataset directly.
    return target['~run'](dataset, config) as OutputDataset<
      RecurOutput,
      RecurIssue
    >;
  },
};

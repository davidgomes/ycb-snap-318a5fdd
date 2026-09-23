import type { BaseIssue, BaseSchema, InferIssue } from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import type { RecurConfig } from './recur.ts';
import type { ResolveRecur } from './types.ts';

/**
 * Recursive schema interface.
 */
export interface RecursiveSchema<
  TWrapped extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
> extends BaseSchema<
    ResolveRecur<TWrapped, 'input'>,
    ResolveRecur<TWrapped, 'output'>,
    InferIssue<TWrapped>
  > {
  /**
   * The schema type.
   */
  readonly type: 'recursive';
  /**
   * The schema reference.
   */
  readonly reference: typeof recursive;
  /**
   * The expected property.
   */
  readonly expects: TWrapped['expects'];
  /**
   * The wrapped schema.
   */
  readonly wrapped: TWrapped;
}

/**
 * Creates a recursive schema that resolves `Recur` placeholders to itself.
 *
 * @param wrapped The wrapped schema.
 *
 * @returns A recursive schema.
 */
// @__NO_SIDE_EFFECTS__
export function recursive<
  const TWrapped extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
>(wrapped: TWrapped): RecursiveSchema<TWrapped> {
  return {
    kind: 'schema',
    type: 'recursive',
    reference: recursive,
    expects: wrapped.expects,
    async: false,
    wrapped,
    get '~standard'() {
      return _getStandardProps(this);
    },
    '~run'(dataset, config) {
      return this.wrapped['~run'](dataset, {
        ...config,
        '~recur': this,
      } as RecurConfig);
    },
  } as RecursiveSchema<TWrapped>;
}

import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  InferIssue,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import type { RecurConfig } from './recur.ts';
import type { ResolveRecur } from './types.ts';

/**
 * Recursive schema async interface.
 */
export interface RecursiveSchemaAsync<
  TWrapped extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> extends BaseSchemaAsync<
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
  readonly reference: typeof recursiveAsync;
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
export function recursiveAsync<
  const TWrapped extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
>(wrapped: TWrapped): RecursiveSchemaAsync<TWrapped> {
  return {
    kind: 'schema',
    type: 'recursive',
    reference: recursiveAsync,
    expects: wrapped.expects,
    async: true,
    wrapped,
    get '~standard'() {
      return _getStandardProps(this);
    },
    async '~run'(dataset, config) {
      return this.wrapped['~run'](dataset, {
        ...config,
        '~recur': this,
      } as RecurConfig);
    },
  } as RecursiveSchemaAsync<TWrapped>;
}

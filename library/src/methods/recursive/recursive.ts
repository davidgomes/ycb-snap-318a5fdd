import type { BaseIssue, BaseSchema, InferIssue } from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import type { RecurSelfInput, RecurSelfOutput } from './types.ts';
import { _withRecurRoot } from './utils.ts';

/**
 * Recursive schema interface.
 */
export interface RecursiveSchema<
  TWrapped extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
> extends Omit<
    BaseSchema<unknown, unknown, InferIssue<TWrapped>>,
    'type' | 'reference' | 'expects' | '~types'
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
  /**
   * The input, output and issue type.
   *
   * @internal
   */
  readonly '~types'?:
    | {
        readonly input: RecurSelfInput<TWrapped>;
        readonly output: RecurSelfOutput<TWrapped>;
        readonly issue: InferIssue<TWrapped>;
      }
    | undefined;
}

/**
 * Creates a recursive schema.
 *
 * Hint: Place `Recur` inside the composed schema to mark self-references, then
 * wrap the finished schema with `recursive` to resolve them.
 *
 * @param schema The schema with `Recur` placeholders.
 *
 * @returns A recursive schema.
 */
// @__NO_SIDE_EFFECTS__
export function recursive<
  const TSchema extends BaseSchema<unknown, unknown, BaseIssue<unknown>>,
>(schema: TSchema): RecursiveSchema<TSchema> {
  return {
    kind: 'schema',
    type: 'recursive',
    reference: recursive,
    expects: schema.expects,
    async: false,
    wrapped: schema,
    get '~standard'() {
      return _getStandardProps(this);
    },
    '~run'(dataset, config) {
      // Parse wrapped schema with recur root
      return schema['~run'](dataset, _withRecurRoot(config, this));
    },
  };
}

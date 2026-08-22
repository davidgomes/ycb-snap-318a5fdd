import type {
  BaseIssue,
  BaseSchema,
  BaseSchemaAsync,
  InferIssue,
} from '../../types/index.ts';
import { _getStandardProps } from '../../utils/index.ts';
import type { recursive } from './recursive.ts';
import type { RecurSelfInput, RecurSelfOutput } from './types.ts';
import { _withRecurRoot } from './utils.ts';

/**
 * Recursive schema async interface.
 */
export interface RecursiveSchemaAsync<
  TWrapped extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
> extends Omit<
    BaseSchemaAsync<unknown, unknown, InferIssue<TWrapped>>,
    'type' | 'reference' | 'expects' | '~types'
  > {
  /**
   * The schema type.
   */
  readonly type: 'recursive';
  /**
   * The schema reference.
   */
  readonly reference: typeof recursive | typeof recursiveAsync;
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
 * wrap the finished schema with `recursiveAsync` to resolve them.
 *
 * @param schema The schema with `Recur` placeholders.
 *
 * @returns A recursive schema.
 */
// @__NO_SIDE_EFFECTS__
export function recursiveAsync<
  const TSchema extends
    | BaseSchema<unknown, unknown, BaseIssue<unknown>>
    | BaseSchemaAsync<unknown, unknown, BaseIssue<unknown>>,
>(schema: TSchema): RecursiveSchemaAsync<TSchema> {
  return {
    kind: 'schema',
    type: 'recursive',
    reference: recursiveAsync,
    expects: schema.expects,
    async: true,
    wrapped: schema,
    get '~standard'() {
      return _getStandardProps(this);
    },
    async '~run'(dataset, config) {
      // Parse wrapped schema with recur root
      return schema['~run'](dataset, _withRecurRoot(config, this));
    },
  };
}

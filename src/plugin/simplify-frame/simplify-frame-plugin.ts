import type { QueryResult } from '../../driver/database-connection.js'
import type { RootOperationNode } from '../../query-compiler/query-compiler.js'
import type { UnknownRow } from '../../util/type-utils.js'
import type {
  KyselyPlugin,
  PluginTransformQueryArgs,
  PluginTransformResultArgs,
} from '../kysely-plugin.js'
import { SimplifyFrameTransformer } from './simplify-frame-transformer.js'

/**
 * Plugin that removes frame clauses from `over` clauses when they are
 * identical to the frame the database would use implicitly.
 *
 * - With `order by`, `range between unbounded preceding and current row`
 *   (and its shorthand `range unbounded preceding`) is removed.
 * - Without `order by`, `range between unbounded preceding and unbounded following`
 *   is removed.
 *
 * Frames using `rows` or `groups` mode, frames with an `exclude` clause and
 * frames with any other bounds are kept as is.
 *
 * ### Examples
 *
 * ```ts
 * import { SimplifyFramePlugin } from 'kysely'
 *
 * const result = await db
 *   .withPlugin(new SimplifyFramePlugin())
 *   .selectFrom('person')
 *   .select((eb) =>
 *     eb.fn
 *       .sum<number>('age')
 *       .over((ob) =>
 *         ob
 *           .orderBy('id')
 *           .range((fb) => fb.betweenUnboundedPreceding().andCurrentRow())
 *       )
 *       .as('running_total_age')
 *   )
 *   .execute()
 * ```
 *
 * The generated SQL (PostgreSQL):
 *
 * ```sql
 * select sum("age") over(order by "id") as "running_total_age"
 * from "person"
 * ```
 */
export class SimplifyFramePlugin implements KyselyPlugin {
  readonly #transformer = new SimplifyFrameTransformer()

  transformQuery(args: PluginTransformQueryArgs): RootOperationNode {
    return this.#transformer.transformNode(args.node, args.queryId)
  }

  transformResult(
    args: PluginTransformResultArgs,
  ): Promise<QueryResult<UnknownRow>> {
    return Promise.resolve(args.result)
  }
}

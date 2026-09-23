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
 * Removes window-frame extents that only repeat the SQL-standard default.
 *
 * - With `order by`, the default is
 *   `range between unbounded preceding and current row`.
 * - Without `order by`, the default is
 *   `range between unbounded preceding and unbounded following`.
 *
 * Frames that use `rows` or `groups`, an `exclude` clause, a non-default
 * bound, or an expression offset are preserved.
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
 *           .range((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
 *       )
 *       .as('running_age'),
 *   )
 *   .execute()
 * ```
 *
 * The generated SQL (PostgreSQL):
 *
 * ```sql
 * select sum("age") over(order by "id") as "running_age"
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

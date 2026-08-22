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
 * Plugin that removes OVER-clause frame extents that just restate the
 * SQL-standard implicit defaults.
 *
 * When an OVER clause contains `ORDER BY`, the database implicitly applies
 * `RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW`. When an OVER clause
 * has no `ORDER BY`, the implicit default is
 * `RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING`.
 *
 * Extents that use `ROWS` or `GROUPS` mode, carry an exclusion clause, or
 * have non-default bound types / expression-based offsets are left intact.
 *
 * ### Examples
 *
 * ```ts
 * import { Kysely, SimplifyFramePlugin, SqliteDialect } from 'kysely'
 * import Sqlite from 'better-sqlite3'
 * import type { Database } from 'type-editor' // imaginary module
 *
 * const db = new Kysely<Database>({
 *   dialect: new SqliteDialect({
 *     database: new Sqlite(':memory:'),
 *   }),
 *   plugins: [new SimplifyFramePlugin()],
 * })
 *
 * const query = db
 *   .selectFrom('person')
 *   .select((eb) =>
 *     eb.fn
 *       .sum<number>('id')
 *       .over((ob) =>
 *         ob.orderBy('id').range((rb) =>
 *           rb.betweenUnboundedPreceding().andCurrentRow(),
 *         ),
 *       )
 *       .as('running_sum'),
 *   )
 *
 * // The generated SQL omits the redundant RANGE extent:
 * // select sum("id") over(order by "id") as "running_sum" from "person"
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

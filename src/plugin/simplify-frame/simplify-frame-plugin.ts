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
 * Plugin that removes window frame clauses that only restate the SQL-standard
 * implicit default frame.
 *
 * An `over` clause with an `order by` implicitly uses
 * `range between unbounded preceding and current row`, and one without an
 * `order by` implicitly uses
 * `range between unbounded preceding and unbounded following`. Frames that
 * spell out exactly these defaults are dropped. Frames in `rows` or `groups`
 * mode, frames with an exclusion, and frames with any other bounds or with
 * offsets are kept as-is.
 *
 * ### Examples
 *
 * ```ts
 * import Sqlite from 'better-sqlite3'
 * import { Kysely, SimplifyFramePlugin, SqliteDialect } from 'kysely'
 * import type { Database } from 'type-editor' // imaginary module
 *
 * const db = new Kysely<Database>({
 *   dialect: new SqliteDialect({
 *     database: new Sqlite(':memory:'),
 *   }),
 *   plugins: [new SimplifyFramePlugin()],
 * })
 *
 * await db
 *   .selectFrom('person')
 *   .select((eb) =>
 *     eb.fn
 *       .sum<number>('age')
 *       .over((ob) =>
 *         ob
 *           .orderBy('id')
 *           .range((frame) => frame.betweenUnboundedPreceding().andCurrentRow()),
 *       )
 *       .as('running_age_total'),
 *   )
 *   .execute()
 * ```
 *
 * The generated SQL (SQLite):
 *
 * ```sql
 * select sum("age") over(order by "id") as "running_age_total"
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

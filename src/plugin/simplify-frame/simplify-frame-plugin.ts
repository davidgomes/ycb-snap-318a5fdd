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
 * Plugin that removes redundant frame extents from `over` clauses.
 *
 * A frame extent is redundant when it replicates the SQL-standard implicit default:
 *
 * - With `order by`: `range between unbounded preceding and current row`
 *   (or its shorthand `range unbounded preceding`).
 * - Without `order by`: `range between unbounded preceding and unbounded following`.
 *
 * Extents using `rows` or `groups` mode, an exclusion clause, other bound types or
 * offsets are left untouched.
 *
 * ### Examples
 *
 * ```ts
 * import { Kysely, PostgresDialect, SimplifyFramePlugin } from 'kysely'
 * import { Pool } from 'pg'
 * import type { Database } from 'type-editor' // imaginary module
 *
 * const db = new Kysely<Database>({
 *   dialect: new PostgresDialect({ pool: new Pool() }),
 *   plugins: [new SimplifyFramePlugin()],
 * })
 *
 * await db
 *   .selectFrom('person')
 *   .select((eb) =>
 *     eb.fn.sum<number>('age').over(
 *       (ob) => ob.orderBy('id').range((fb) => fb.betweenUnboundedPreceding().andCurrentRow())
 *     ).as('running_age')
 *   )
 *   .execute()
 * ```
 *
 * The generated SQL (PostgreSQL):
 *
 * ```sql
 * select sum("age") over(order by "id") as "running_age" from "person"
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

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
 * Removes `over` frame extents that only restate the SQL-standard default.
 *
 * When `order by` is present the default is
 * `range between unbounded preceding and current row`.
 * Without `order by` the default is
 * `range between unbounded preceding and unbounded following`.
 *
 * Extents that use `rows` or `groups`, an `exclude` clause, a non-default
 * bound, or an expression offset are preserved.
 *
 * ### Examples
 *
 * ```ts
 * import { Kysely, PostgresDialect, SimplifyFramePlugin } from 'kysely'
 *
 * const db = new Kysely({
 *   dialect: new PostgresDialect({ ... }),
 *   plugins: [new SimplifyFramePlugin()],
 * })
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

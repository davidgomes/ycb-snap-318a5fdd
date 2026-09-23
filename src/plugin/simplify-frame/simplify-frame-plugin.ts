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
 * Removes window-frame extents that only restate the SQL-standard default.
 *
 * When an `over` clause has `order by`, the implicit frame is
 * `range between unbounded preceding and current row`.
 * When it has no `order by`, the implicit frame is
 * `range between unbounded preceding and unbounded following`.
 *
 * Extents that use `rows` or `groups`, an exclusion clause, a non-default
 * bound, or an expression offset are preserved.
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

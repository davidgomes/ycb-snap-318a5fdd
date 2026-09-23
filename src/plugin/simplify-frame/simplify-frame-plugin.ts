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
 * Plugin that removes window frame clauses that are equal to the SQL
 * standard's implicit default frame:
 *
 * - `range between unbounded preceding and current row` when the `over`
 *   clause has an `order by`.
 * - `range between unbounded preceding and unbounded following` when the
 *   `over` clause has no `order by`.
 *
 * `rows` and `groups` frames, frames with an `exclude` clause, and frames
 * with any other bounds are kept as is.
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

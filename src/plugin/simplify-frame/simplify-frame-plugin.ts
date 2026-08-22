import type { QueryResult } from '../../driver/database-connection.js'
import type { FrameBoundNode } from '../../operation-node/frame-bound-node.js'
import type { FrameNode } from '../../operation-node/frame-node.js'
import { OperationNodeTransformer } from '../../operation-node/operation-node-transformer.js'
import { OverNode } from '../../operation-node/over-node.js'
import type { RootOperationNode } from '../../query-compiler/query-compiler.js'
import type { QueryId } from '../../util/query-id.js'
import type { UnknownRow } from '../../util/type-utils.js'
import type {
  KyselyPlugin,
  PluginTransformQueryArgs,
  PluginTransformResultArgs,
} from '../kysely-plugin.js'

/**
 * Plugin that strips `OVER` clause extent specifications that replicate
 * the SQL-standard implicit defaults.
 *
 * - When an `OVER` clause contains `ORDER BY`, the database implicitly applies
 *   `RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW`.
 * - When an `OVER` clause has no `ORDER BY`, the implicit default is
 *   `RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING`.
 *
 * Extents that use `ROWS` or `GROUPS` mode, carry an exclusion clause, or
 * have non-default bound types / expression-based offsets are preserved.
 *
 * ### Examples
 *
 * ```ts
 * import { Kysely, PostgresDialect, SimplifyFramePlugin } from 'kysely'
 *
 * const db = new Kysely<DB>({
 *   dialect: new PostgresDialect({ pool }),
 *   plugins: [new SimplifyFramePlugin()],
 * })
 *
 * const query = db
 *   .selectFrom('person')
 *   .select((eb) =>
 *     eb.fn
 *       .sum<number>('id')
 *       .over((ob) =>
 *         ob.orderBy('first_name').range((rb) =>
 *           rb.betweenUnboundedPreceding().andCurrentRow(),
 *         ),
 *       )
 *       .as('running_sum'),
 *   )
 *
 * // Compiled without the redundant RANGE extent:
 * // select sum("id") over(order by "first_name") as "running_sum" from "person"
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

export class SimplifyFrameTransformer extends OperationNodeTransformer {
  protected override transformOver(
    node: OverNode,
    queryId?: QueryId,
  ): OverNode {
    const transformed = super.transformOver(node, queryId)

    if (isRedundantDefaultExtent(transformed)) {
      return OverNode.cloneWithoutFrame(transformed)
    }

    return transformed
  }
}

function isRedundantDefaultExtent(node: OverNode): boolean {
  const frame = node.frame

  if (!frame || !isDefaultRangeExtent(frame)) {
    return false
  }

  if (node.orderBy) {
    return isCurrentRowBound(frame.end) || frame.end === undefined
  }

  return isUnboundedFollowingBound(frame.end)
}

function isDefaultRangeExtent(frame: FrameNode): boolean {
  return (
    frame.mode === 'range' &&
    frame.exclusion === undefined &&
    isUnboundedPrecedingBound(frame.start)
  )
}

function isUnboundedPrecedingBound(
  bound: FrameBoundNode | undefined,
): bound is FrameBoundNode {
  return (
    bound !== undefined &&
    bound.type === 'unbounded preceding' &&
    bound.offset === undefined
  )
}

function isCurrentRowBound(bound: FrameBoundNode | undefined): boolean {
  return (
    bound !== undefined &&
    bound.type === 'current row' &&
    bound.offset === undefined
  )
}

function isUnboundedFollowingBound(bound: FrameBoundNode | undefined): boolean {
  return (
    bound !== undefined &&
    bound.type === 'unbounded following' &&
    bound.offset === undefined
  )
}

import { OperationNodeTransformer } from '../../operation-node/operation-node-transformer.js'
import type {
  OverNode,
  WindowFrameBound,
} from '../../operation-node/over-node.js'
import { freeze } from '../../util/object-utils.js'
import type { QueryId } from '../../util/query-id.js'

/**
 * Drops window extents that repeat the SQL-standard implicit frame.
 *
 * When `order by` is present the implicit frame is
 * `range between unbounded preceding and current row`
 * (also written `range unbounded preceding`).
 *
 * When `order by` is absent the implicit frame is
 * `range between unbounded preceding and unbounded following`.
 *
 * `rows` and `groups` modes, exclusion clauses, non-default bounds, and
 * expression offsets are left untouched.
 */
export class SimplifyFrameTransformer extends OperationNodeTransformer {
  protected override transformOver(
    node: OverNode,
    queryId?: QueryId,
  ): OverNode {
    const transformed = super.transformOver(node, queryId)

    if (!isRedundantWindowFrame(transformed)) {
      return transformed
    }

    return freeze({
      ...transformed,
      frame: undefined,
    })
  }
}

function isRedundantWindowFrame(node: OverNode): boolean {
  const frame = node.frame

  if (!frame || frame.mode !== 'range' || frame.exclusion) {
    return false
  }

  if (hasOffset(frame.start) || hasOffset(frame.end)) {
    return false
  }

  if (frame.start.type !== 'unbounded preceding') {
    return false
  }

  const hasOrderBy = !!node.orderBy && node.orderBy.items.length > 0

  if (hasOrderBy) {
    // `range unbounded preceding` is defined as
    // `range between unbounded preceding and current row`.
    return !frame.end || frame.end.type === 'current row'
  }

  return frame.end?.type === 'unbounded following'
}

function hasOffset(bound: WindowFrameBound | undefined): boolean {
  return bound?.offset !== undefined
}

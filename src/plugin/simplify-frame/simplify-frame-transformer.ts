import { OperationNodeTransformer } from '../../operation-node/operation-node-transformer.js'
import type { OverNode } from '../../operation-node/over-node.js'
import type { WindowFrameBoundNode } from '../../operation-node/window-frame-bound-node.js'
import type { QueryId } from '../../util/query-id.js'

/**
 * Strips OVER-clause extents that just restate SQL-standard implicit defaults.
 */
export class SimplifyFrameTransformer extends OperationNodeTransformer {
  protected override transformOver(
    node: OverNode,
    queryId?: QueryId,
  ): OverNode {
    const transformed = super.transformOver(node, queryId)

    if (!transformed.frame || !isRedundantDefaultFrame(transformed)) {
      return transformed
    }

    return {
      ...transformed,
      frame: undefined,
    }
  }
}

function isRedundantDefaultFrame(over: OverNode): boolean {
  const frame = over.frame

  if (!frame) {
    return false
  }

  // Only RANGE extents without an exclusion can match the implicit defaults.
  if (frame.mode !== 'range' || frame.exclusion !== undefined) {
    return false
  }

  if (!isDefaultStartBound(frame.start)) {
    return false
  }

  if (over.orderBy) {
    // Implicit default with ORDER BY:
    // RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
    // (also the single-bound shorthand RANGE UNBOUNDED PRECEDING)
    return frame.end === undefined || isCurrentRowBound(frame.end)
  }

  // Implicit default without ORDER BY:
  // RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING
  return frame.end !== undefined && isUnboundedFollowingBound(frame.end)
}

function isDefaultStartBound(bound: WindowFrameBoundNode): boolean {
  return bound.boundType === 'unbounded preceding' && bound.offset === undefined
}

function isCurrentRowBound(bound: WindowFrameBoundNode): boolean {
  return bound.boundType === 'current row' && bound.offset === undefined
}

function isUnboundedFollowingBound(bound: WindowFrameBoundNode): boolean {
  return bound.boundType === 'unbounded following' && bound.offset === undefined
}

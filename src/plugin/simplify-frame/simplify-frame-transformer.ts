import type { FrameBoundNode } from '../../operation-node/frame-bound-node.js'
import type { FrameNode } from '../../operation-node/frame-node.js'
import { OperationNodeTransformer } from '../../operation-node/operation-node-transformer.js'
import type { OverNode } from '../../operation-node/over-node.js'
import type { QueryId } from '../../util/query-id.js'
import { freeze } from '../../util/object-utils.js'

export class SimplifyFrameTransformer extends OperationNodeTransformer {
  protected override transformOver(node: OverNode, queryId?: QueryId): OverNode {
    const transformed = super.transformOver(node, queryId)

    if (
      !transformed.frame ||
      !isImplicitDefault(transformed.frame, !!transformed.orderBy)
    ) {
      return transformed
    }

    const { frame: _, ...rest } = transformed

    return freeze(rest)
  }
}

function isImplicitDefault(frame: FrameNode, hasOrderBy: boolean): boolean {
  if (frame.mode !== 'range' || frame.exclusion) {
    return false
  }

  if (!isBound(frame.start, 'unbounded preceding')) {
    return false
  }

  if (hasOrderBy) {
    // `range unbounded preceding` is shorthand for `range between unbounded preceding and current row`.
    return !frame.end || isBound(frame.end, 'current row')
  }

  return !!frame.end && isBound(frame.end, 'unbounded following')
}

function isBound(
  bound: FrameBoundNode,
  type: 'unbounded preceding' | 'current row' | 'unbounded following',
): boolean {
  return bound.type === type && !bound.offset
}

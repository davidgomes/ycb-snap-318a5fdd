import type { FrameBound, FrameNode } from '../../operation-node/frame-node.js'
import { OperationNodeTransformer } from '../../operation-node/operation-node-transformer.js'
import type { OverNode } from '../../operation-node/over-node.js'
import type { QueryId } from '../../util/query-id.js'
import { freeze } from '../../util/object-utils.js'

export class SimplifyFrameTransformer extends OperationNodeTransformer {
  protected override transformOver(
    node: OverNode,
    queryId?: QueryId,
  ): OverNode {
    const transformed = super.transformOver(node, queryId)

    if (!transformed.frame || !isImplicitDefault(transformed)) {
      return transformed
    }

    const { frame: _, ...rest } = transformed
    return freeze(rest)
  }
}

function isImplicitDefault(node: OverNode): boolean {
  const frame = node.frame as FrameNode

  if (frame.mode !== 'range' || frame.exclude) {
    return false
  }

  if (!isPlainBound(frame.start, 'unbounded preceding')) {
    return false
  }

  if (node.orderBy) {
    return !frame.end || isPlainBound(frame.end, 'current row')
  }

  return !!frame.end && isPlainBound(frame.end, 'unbounded following')
}

function isPlainBound(bound: FrameBound, type: FrameBound['type']): boolean {
  return bound.type === type && !bound.offset
}

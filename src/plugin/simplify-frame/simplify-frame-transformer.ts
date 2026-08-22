import type { AggregateFunctionNode } from '../../operation-node/aggregate-function-node.js'
import type { OverNode } from '../../operation-node/over-node.js'
import type { WindowFrameBoundNode } from '../../operation-node/window-frame-bound-node.js'
import type { WindowFrameNode } from '../../operation-node/window-frame-node.js'
import { OperationNodeTransformer } from '../../operation-node/operation-node-transformer.js'
import { freeze } from '../../util/object-utils.js'
import type { QueryId } from '../../util/query-id.js'

function isDefaultStartBound(node: WindowFrameBoundNode): boolean {
  return node.boundType === 'unbounded preceding' && !node.offset
}

function isDefaultEndBoundWithOrderBy(node: WindowFrameBoundNode): boolean {
  return node.boundType === 'current row' && !node.offset
}

function isDefaultEndBoundWithoutOrderBy(node: WindowFrameBoundNode): boolean {
  return node.boundType === 'unbounded following' && !node.offset
}

function isRedundantFrame(over: OverNode, frame: WindowFrameNode): boolean {
  if (frame.mode !== 'range' || frame.exclusion) {
    return false
  }

  if (!isDefaultStartBound(frame.start)) {
    return false
  }

  if (!frame.end) {
    return false
  }

  if (over.orderBy) {
    return isDefaultEndBoundWithOrderBy(frame.end)
  }

  return isDefaultEndBoundWithoutOrderBy(frame.end)
}

export class SimplifyFrameTransformer extends OperationNodeTransformer {
  protected override transformAggregateFunction(
    node: AggregateFunctionNode,
    queryId?: QueryId,
  ): AggregateFunctionNode {
    const transformed = super.transformAggregateFunction(node, queryId)

    if (!transformed.over?.frame) {
      return transformed
    }

    if (!isRedundantFrame(transformed.over, transformed.over.frame)) {
      return transformed
    }

    return freeze({
      ...transformed,
      over: freeze({
        ...transformed.over,
        frame: undefined,
      }),
    })
  }
}

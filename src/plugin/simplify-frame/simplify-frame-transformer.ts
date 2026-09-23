import type { FrameBoundNode } from '../../operation-node/frame-bound-node.js'
import { OperationNodeTransformer } from '../../operation-node/operation-node-transformer.js'
import { OverNode } from '../../operation-node/over-node.js'
import type { QueryId } from '../../util/query-id.js'

export class SimplifyFrameTransformer extends OperationNodeTransformer {
  protected override transformOver(
    node: OverNode,
    queryId?: QueryId,
  ): OverNode {
    const transformed = super.transformOver(node, queryId)

    return isImplicitDefaultFrame(transformed)
      ? OverNode.cloneWithoutFrame(transformed)
      : transformed
  }
}

/**
 * With an `order by`, the implicit frame is `range between unbounded preceding
 * and current row`. Without one, all rows are peers and the implicit frame is
 * `range between unbounded preceding and unbounded following`.
 */
function isImplicitDefaultFrame(node: OverNode): boolean {
  const { frame } = node

  if (!frame || frame.mode !== 'range' || frame.exclusion) {
    return false
  }

  // The single-bound form implicitly ends at the current row.
  const end = frame.end ?? { type: 'current row' }

  return (
    isOffsetless(frame.start, 'unbounded preceding') &&
    isOffsetless(end, node.orderBy ? 'current row' : 'unbounded following')
  )
}

function isOffsetless(
  bound: Pick<FrameBoundNode, 'type' | 'offset'>,
  type: FrameBoundNode['type'],
): boolean {
  return bound.type === type && !bound.offset
}

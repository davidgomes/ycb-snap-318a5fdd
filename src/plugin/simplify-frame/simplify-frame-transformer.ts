import type { FrameNode } from '../../operation-node/frame-node.js'
import { OperationNodeTransformer } from '../../operation-node/operation-node-transformer.js'
import type { OverNode } from '../../operation-node/over-node.js'
import { freeze } from '../../util/object-utils.js'
import type { QueryId } from '../../util/query-id.js'

export class SimplifyFrameTransformer extends OperationNodeTransformer {
  protected override transformOver(
    node: OverNode,
    queryId?: QueryId,
  ): OverNode {
    const transformed = super.transformOver(node, queryId)

    if (
      !transformed.frame ||
      !isImplicitDefaultFrame(
        transformed.frame,
        !!transformed.orderBy && transformed.orderBy.items.length > 0,
      )
    ) {
      return transformed
    }

    return freeze({ ...transformed, frame: undefined })
  }
}

/**
 * Without `order by` the SQL standard default frame is
 * `range between unbounded preceding and unbounded following`, with `order by`
 * it is `range between unbounded preceding and current row`.
 */
function isImplicitDefaultFrame(
  frame: FrameNode,
  hasOrderBy: boolean,
): boolean {
  if (frame.mode !== 'range' || frame.exclusion) {
    return false
  }

  const { start, end } = frame

  if (start.type !== 'unbounded preceding' || start.offset) {
    return false
  }

  // A single bound frame `range <start>` means `range between <start> and current row`.
  const endType = end ? end.type : 'current row'

  if (end?.offset) {
    return false
  }

  return endType === (hasOrderBy ? 'current row' : 'unbounded following')
}

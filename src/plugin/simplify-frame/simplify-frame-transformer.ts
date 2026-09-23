import type { FrameBoundNode } from '../../operation-node/frame-bound-node.js'
import { OperationNodeTransformer } from '../../operation-node/operation-node-transformer.js'
import type { OverNode } from '../../operation-node/over-node.js'
import { ValueNode } from '../../operation-node/value-node.js'
import { freeze } from '../../util/object-utils.js'
import type { QueryId } from '../../util/query-id.js'

/**
 * Drops window frame extents that repeat the SQL-standard implicit default.
 *
 * - `ORDER BY` present: `RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW`
 *   (including the single-bound shorthand `RANGE UNBOUNDED PRECEDING`).
 * - No `ORDER BY`: `RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING`.
 *
 * `ROWS` / `GROUPS`, exclusion clauses, non-default bounds, and expression
 * offsets are left untouched.
 */
export class SimplifyFrameTransformer extends OperationNodeTransformer {
  protected override transformOver(
    node: OverNode,
    queryId?: QueryId,
  ): OverNode {
    const transformed = super.transformOver(node, queryId)

    if (!transformed.frame || !isRedundantExtent(transformed)) {
      return transformed
    }

    return freeze({
      ...transformed,
      frame: undefined,
    })
  }
}

function isRedundantExtent(node: OverNode): boolean {
  const frame = node.frame

  if (!frame || frame.unit !== 'range' || frame.exclusion) {
    return false
  }

  if (
    isExpressionOffset(frame.start) ||
    (frame.end !== undefined && isExpressionOffset(frame.end))
  ) {
    return false
  }

  const hasOrderBy = !!node.orderBy && node.orderBy.items.length > 0

  if (hasOrderBy) {
    if (!isKeywordBound(frame.start, 'unbounded preceding')) {
      return false
    }

    return frame.end === undefined || isKeywordBound(frame.end, 'current row')
  }

  return (
    frame.end !== undefined &&
    isKeywordBound(frame.start, 'unbounded preceding') &&
    isKeywordBound(frame.end, 'unbounded following')
  )
}

function isKeywordBound(
  bound: FrameBoundNode,
  expected: FrameBoundNode['bound'],
): boolean {
  return bound.bound === expected && bound.offset === undefined
}

function isExpressionOffset(bound: FrameBoundNode): boolean {
  return bound.offset !== undefined && !ValueNode.is(bound.offset)
}

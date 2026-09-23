import { FrameBoundNode } from '../../operation-node/frame-bound-node.js'
import type { FrameBoundType } from '../../operation-node/frame-bound-node.js'
import type { FrameNode } from '../../operation-node/frame-node.js'
import { OperationNodeTransformer } from '../../operation-node/operation-node-transformer.js'
import type { OverNode } from '../../operation-node/over-node.js'
import { ValueNode } from '../../operation-node/value-node.js'
import { requireAllProps } from '../../util/require-all-props.js'
import type { QueryId } from '../../util/query-id.js'

/**
 * Drops window-frame extents that repeat the SQL-standard implicit default.
 *
 * With `order by`, that default is
 * `range between unbounded preceding and current row`
 * (including the shorthand `range unbounded preceding`).
 * Without `order by`, it is
 * `range between unbounded preceding and unbounded following`.
 *
 * `rows` and `groups` frames, exclusion clauses, non-default bounds, and
 * expression offsets are left untouched.
 */
export class SimplifyFrameTransformer extends OperationNodeTransformer {
  protected override transformOver(
    node: OverNode,
    queryId?: QueryId,
  ): OverNode {
    const transformed = super.transformOver(node, queryId)

    if (!transformed.frame || !isRedundantFrame(transformed)) {
      return transformed
    }

    return requireAllProps<OverNode>({
      kind: 'OverNode',
      orderBy: transformed.orderBy,
      partitionBy: transformed.partitionBy,
      frame: undefined,
    })
  }
}

function isRedundantFrame(over: OverNode): boolean {
  const frame = over.frame

  if (!frame || frame.mode !== 'range' || frame.exclusion) {
    return false
  }

  const bounds = resolveBounds(frame)

  if (bounds.expressionOffset || bounds.valueOffset) {
    return false
  }

  if (over.orderBy) {
    return (
      bounds.start === 'unbounded preceding' && bounds.end === 'current row'
    )
  }

  return (
    bounds.start === 'unbounded preceding' &&
    bounds.end === 'unbounded following'
  )
}

function resolveBounds(frame: FrameNode): {
  start: FrameBoundType
  end: FrameBoundType
  expressionOffset: boolean
  valueOffset: boolean
} {
  const expressionOffset =
    isExpressionOffset(frame.start) ||
    (frame.end ? isExpressionOffset(frame.end) : false)
  const valueOffset =
    hasValueOffset(frame.start) ||
    (frame.end ? hasValueOffset(frame.end) : false)

  if (frame.end) {
    return {
      start: frame.start.bound,
      end: frame.end.bound,
      expressionOffset,
      valueOffset,
    }
  }

  // SQL expands a single bound into a between pair ending or starting at
  // current row. `unbounded preceding` is the ordered-window default.
  switch (frame.start.bound) {
    case 'unbounded preceding':
      return {
        start: 'unbounded preceding',
        end: 'current row',
        expressionOffset,
        valueOffset,
      }
    case 'preceding':
      return {
        start: 'preceding',
        end: 'current row',
        expressionOffset,
        valueOffset,
      }
    case 'current row':
      return {
        start: 'current row',
        end: 'current row',
        expressionOffset,
        valueOffset,
      }
    case 'following':
      return {
        start: 'current row',
        end: 'following',
        expressionOffset,
        valueOffset,
      }
    case 'unbounded following':
      return {
        start: 'current row',
        end: 'unbounded following',
        expressionOffset,
        valueOffset,
      }
  }
}

function isExpressionOffset(bound: FrameBoundNode): boolean {
  return bound.offset !== undefined && !ValueNode.is(bound.offset)
}

function hasValueOffset(bound: FrameBoundNode): boolean {
  return bound.offset !== undefined && ValueNode.is(bound.offset)
}

import { OperationNodeTransformer } from '../../operation-node/operation-node-transformer.js'
import type {
  OverFrame,
  OverFrameBound,
  OverNode,
} from '../../operation-node/over-node.js'
import { freeze } from '../../util/object-utils.js'
import type { QueryId } from '../../util/query-id.js'

/**
 * Drops window-frame extents that repeat the SQL-standard implicit default.
 *
 * - `order by` present: `range between unbounded preceding and current row`
 *   (including the `range unbounded preceding` shorthand).
 * - no `order by`: `range between unbounded preceding and unbounded following`.
 *
 * `rows` and `groups` extents, exclusion clauses, non-default bounds, and
 * expression offsets are left in place. Numeric offsets are also kept, because
 * a parameterized offset is not an implicit bound.
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

    const { frame: _frame, ...rest } = transformed
    return freeze(rest)
  }
}

function isRedundantFrame(node: OverNode): boolean {
  const frame = node.frame

  if (!frame || frame.mode !== 'range' || frame.exclusion) {
    return false
  }

  if (boundHasOffset(frame.start) || boundHasOffset(frame.end)) {
    return false
  }

  const hasOrderBy = !!node.orderBy && node.orderBy.items.length > 0

  if (hasOrderBy) {
    return isOrderedDefault(frame)
  }

  return isUnorderedDefault(frame)
}

function isOrderedDefault(frame: OverFrame): boolean {
  if (frame.start.kind !== 'unbounded preceding') {
    return false
  }

  return frame.end === undefined || frame.end.kind === 'current row'
}

function isUnorderedDefault(frame: OverFrame): boolean {
  return (
    frame.start.kind === 'unbounded preceding' &&
    frame.end?.kind === 'unbounded following'
  )
}

function boundHasOffset(bound: OverFrameBound | undefined): boolean {
  // Parameterized numbers and expression offsets are both explicit bounds.
  // Implicit defaults never carry an offset.
  return bound?.offset !== undefined
}

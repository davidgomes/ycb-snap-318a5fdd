import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'
import type { OrderByItemNode } from './order-by-item-node.js'
import { OrderByNode } from './order-by-node.js'
import type { PartitionByItemNode } from './partition-by-item-node.js'
import { PartitionByNode } from './partition-by-node.js'

export type WindowFrameMode = 'rows' | 'range' | 'groups'

export type WindowFrameBoundType =
  | 'unbounded preceding'
  | 'preceding'
  | 'current row'
  | 'following'
  | 'unbounded following'

export type WindowFrameExclusion =
  | 'current row'
  | 'group'
  | 'ties'
  | 'no others'

export interface WindowFrameBound {
  readonly type: WindowFrameBoundType
  readonly offset?: OperationNode
}

/**
 * Window frame (extent) attached to an `over` clause.
 *
 * `end` is absent for single-bound shorthands such as `rows unbounded preceding`.
 */
export interface WindowFrameClause {
  readonly mode: WindowFrameMode
  readonly start: WindowFrameBound
  readonly end?: WindowFrameBound
  readonly exclusion?: WindowFrameExclusion
}

export interface OverNode extends OperationNode {
  readonly kind: 'OverNode'
  readonly orderBy?: OrderByNode
  readonly partitionBy?: PartitionByNode
  readonly frame?: WindowFrameClause
}

type OverNodeFactory = Readonly<{
  is(node: OperationNode): node is OverNode
  create(): Readonly<OverNode>
  cloneWithOrderByItems(
    overNode: OverNode,
    items: ReadonlyArray<OrderByItemNode>,
  ): Readonly<OverNode>
  cloneWithPartitionByItems(
    overNode: OverNode,
    items: ReadonlyArray<PartitionByItemNode>,
  ): Readonly<OverNode>
  cloneWithFrame(
    overNode: OverNode,
    frame: WindowFrameClause,
  ): Readonly<OverNode>
}>

/**
 * @internal
 */
export const OverNode: OverNodeFactory = freeze<OverNodeFactory>({
  is(node): node is OverNode {
    return node.kind === 'OverNode'
  },

  create() {
    return freeze({
      kind: 'OverNode',
    })
  },

  cloneWithOrderByItems(overNode, items) {
    return freeze({
      ...overNode,
      orderBy: overNode.orderBy
        ? OrderByNode.cloneWithItems(overNode.orderBy, items)
        : OrderByNode.create(items),
    })
  },

  cloneWithPartitionByItems(overNode, items) {
    return freeze({
      ...overNode,
      partitionBy: overNode.partitionBy
        ? PartitionByNode.cloneWithItems(overNode.partitionBy, items)
        : PartitionByNode.create(items),
    })
  },

  cloneWithFrame(overNode, frame) {
    return freeze({
      ...overNode,
      frame,
    })
  },
})

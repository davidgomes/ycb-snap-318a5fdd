import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'
import type { OrderByItemNode } from './order-by-item-node.js'
import { OrderByNode } from './order-by-node.js'
import type { PartitionByItemNode } from './partition-by-item-node.js'
import { PartitionByNode } from './partition-by-node.js'

export type OverFrameMode = 'rows' | 'range' | 'groups'

export type OverFrameBoundKind =
  | 'unbounded preceding'
  | 'preceding'
  | 'current row'
  | 'following'
  | 'unbounded following'

export type OverFrameExclusion = 'current row' | 'group' | 'ties' | 'no others'

export interface OverFrameBound {
  readonly kind: OverFrameBoundKind
  readonly offset?: OperationNode
}

export interface OverFrame {
  readonly mode: OverFrameMode
  readonly start: OverFrameBound
  readonly end?: OverFrameBound
  readonly exclusion?: OverFrameExclusion
}

export interface OverNode extends OperationNode {
  readonly kind: 'OverNode'
  readonly orderBy?: OrderByNode
  readonly partitionBy?: PartitionByNode
  readonly frame?: OverFrame
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
  cloneWithFrame(overNode: OverNode, frame: OverFrame): Readonly<OverNode>
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
      frame: freeze({
        ...frame,
        start: freeze(frame.start),
        end: frame.end ? freeze(frame.end) : undefined,
      }),
    })
  },
})

import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'

export type WindowFrameBoundKind =
  | 'unbounded preceding'
  | 'preceding'
  | 'current row'
  | 'following'
  | 'unbounded following'

export interface WindowFrameBoundNode extends OperationNode {
  readonly kind: 'FrameBoundNode'
  readonly type: WindowFrameBoundKind
  readonly offset?: OperationNode
}

type WindowFrameBoundNodeFactory = Readonly<{
  is(node: OperationNode): node is WindowFrameBoundNode
  create(
    boundType: WindowFrameBoundKind,
    offset?: OperationNode,
  ): Readonly<WindowFrameBoundNode>
}>

/**
 * @internal
 */
export const WindowFrameBoundNode: WindowFrameBoundNodeFactory =
  freeze<WindowFrameBoundNodeFactory>({
    is(node): node is WindowFrameBoundNode {
    return node.kind === 'FrameBoundNode'
    },

    create(boundType, offset) {
      return freeze({
        kind: 'FrameBoundNode',
        type: boundType,
        offset,
      })
    },
  })

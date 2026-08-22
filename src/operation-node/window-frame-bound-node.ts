import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'

export type WindowFrameBoundType =
  | 'unbounded preceding'
  | 'preceding'
  | 'current row'
  | 'following'
  | 'unbounded following'

export interface WindowFrameBoundNode extends OperationNode {
  readonly kind: 'WindowFrameBoundNode'
  readonly boundType: WindowFrameBoundType
  readonly offset?: OperationNode
}

type WindowFrameBoundNodeFactory = Readonly<{
  is(node: OperationNode): node is WindowFrameBoundNode
  create(
    boundType: WindowFrameBoundType,
    offset?: OperationNode,
  ): Readonly<WindowFrameBoundNode>
}>

/**
 * @internal
 */
export const WindowFrameBoundNode: WindowFrameBoundNodeFactory =
  freeze<WindowFrameBoundNodeFactory>({
    is(node): node is WindowFrameBoundNode {
      return node.kind === 'WindowFrameBoundNode'
    },

    create(boundType, offset) {
      return freeze({
        kind: 'WindowFrameBoundNode',
        boundType,
        ...(offset && { offset }),
      })
    },
  })

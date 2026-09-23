import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'

export type FrameBoundKind =
  | 'unbounded preceding'
  | 'preceding'
  | 'current row'
  | 'following'
  | 'unbounded following'

export interface FrameBoundNode extends OperationNode {
  readonly kind: 'FrameBoundNode'
  readonly bound: FrameBoundKind
  readonly offset?: OperationNode
}

type FrameBoundNodeFactory = Readonly<{
  is(node: OperationNode): node is FrameBoundNode
  create(
    bound: FrameBoundKind,
    offset?: OperationNode,
  ): Readonly<FrameBoundNode>
}>

/**
 * @internal
 */
export const FrameBoundNode: FrameBoundNodeFactory =
  freeze<FrameBoundNodeFactory>({
    is(node): node is FrameBoundNode {
      return node.kind === 'FrameBoundNode'
    },

    create(bound, offset) {
      return freeze({
        kind: 'FrameBoundNode',
        bound,
        offset,
      })
    },
  })

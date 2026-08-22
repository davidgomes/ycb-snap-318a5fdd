import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'

export interface GroupByCubeNode extends OperationNode {
  readonly kind: 'GroupByCubeNode'
  readonly columns: ReadonlyArray<OperationNode>
}

type GroupByCubeNodeFactory = Readonly<{
  is(node: OperationNode): node is GroupByCubeNode
  create(columns: ReadonlyArray<OperationNode>): Readonly<GroupByCubeNode>
}>

/**
 * @internal
 */
export const GroupByCubeNode: GroupByCubeNodeFactory =
  freeze<GroupByCubeNodeFactory>({
    is(node): node is GroupByCubeNode {
      return node.kind === 'GroupByCubeNode'
    },

    create(columns) {
      return freeze({
        kind: 'GroupByCubeNode',
        columns: freeze(columns),
      })
    },
  })

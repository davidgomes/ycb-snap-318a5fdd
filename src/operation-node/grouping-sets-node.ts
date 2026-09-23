import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'

export type GroupingSetsType = 'cube' | 'rollup' | 'grouping sets'

export interface GroupingSetsNode extends OperationNode {
  readonly kind: 'GroupingSetsNode'
  readonly type: GroupingSetsType
  readonly items: ReadonlyArray<OperationNode>
}

type GroupingSetsNodeFactory = Readonly<{
  is(node: OperationNode): node is GroupingSetsNode
  create(
    type: GroupingSetsType,
    items: ReadonlyArray<OperationNode>,
  ): Readonly<GroupingSetsNode>
}>

/**
 * @internal
 */
export const GroupingSetsNode: GroupingSetsNodeFactory =
  freeze<GroupingSetsNodeFactory>({
    is(node): node is GroupingSetsNode {
      return node.kind === 'GroupingSetsNode'
    },

    create(type, items) {
      return freeze({
        kind: 'GroupingSetsNode',
        type,
        items: freeze(items),
      })
    },
  })

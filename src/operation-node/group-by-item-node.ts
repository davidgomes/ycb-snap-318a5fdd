import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'

export type GroupByModifier = 'cube' | 'rollup' | 'grouping sets'

export interface GroupByItemNode extends OperationNode {
  readonly kind: 'GroupByItemNode'
  readonly groupBy: OperationNode
  readonly modifier?: GroupByModifier
}

type GroupByItemNodeFactory = Readonly<{
  is(node: OperationNode): node is GroupByItemNode
  create(
    groupBy: OperationNode,
    modifier?: GroupByModifier,
  ): Readonly<GroupByItemNode>
}>

/**
 * @internal
 */
export const GroupByItemNode: GroupByItemNodeFactory =
  freeze<GroupByItemNodeFactory>({
    is(node): node is GroupByItemNode {
      return node.kind === 'GroupByItemNode'
    },

    create(groupBy, modifier) {
      return freeze({
        kind: 'GroupByItemNode',
        groupBy,
        modifier,
      })
    },
  })

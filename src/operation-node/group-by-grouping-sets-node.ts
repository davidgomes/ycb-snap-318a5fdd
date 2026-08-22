import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'
import type { GroupByGroupingSetNode } from './group-by-grouping-set-node.js'

export interface GroupByGroupingSetsNode extends OperationNode {
  readonly kind: 'GroupByGroupingSetsNode'
  readonly sets: ReadonlyArray<GroupByGroupingSetNode>
}

type GroupByGroupingSetsNodeFactory = Readonly<{
  is(node: OperationNode): node is GroupByGroupingSetsNode
  create(
    sets: ReadonlyArray<GroupByGroupingSetNode>,
  ): Readonly<GroupByGroupingSetsNode>
}>

/**
 * @internal
 */
export const GroupByGroupingSetsNode: GroupByGroupingSetsNodeFactory =
  freeze<GroupByGroupingSetsNodeFactory>({
    is(node): node is GroupByGroupingSetsNode {
      return node.kind === 'GroupByGroupingSetsNode'
    },

    create(sets) {
      return freeze({
        kind: 'GroupByGroupingSetsNode',
        sets: freeze(sets),
      })
    },
  })

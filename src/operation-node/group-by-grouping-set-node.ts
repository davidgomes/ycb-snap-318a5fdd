import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'

export interface GroupByGroupingSetNode extends OperationNode {
  readonly kind: 'GroupByGroupingSetNode'
  readonly columns: ReadonlyArray<OperationNode>
}

type GroupByGroupingSetNodeFactory = Readonly<{
  is(node: OperationNode): node is GroupByGroupingSetNode
  create(
    columns: ReadonlyArray<OperationNode>,
  ): Readonly<GroupByGroupingSetNode>
}>

/**
 * @internal
 */
export const GroupByGroupingSetNode: GroupByGroupingSetNodeFactory =
  freeze<GroupByGroupingSetNodeFactory>({
    is(node): node is GroupByGroupingSetNode {
      return node.kind === 'GroupByGroupingSetNode'
    },

    create(columns) {
      return freeze({
        kind: 'GroupByGroupingSetNode',
        columns: freeze(columns),
      })
    },
  })

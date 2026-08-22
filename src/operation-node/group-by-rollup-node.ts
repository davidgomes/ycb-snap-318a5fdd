import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'

export interface GroupByRollupNode extends OperationNode {
  readonly kind: 'GroupByRollupNode'
  readonly columns: ReadonlyArray<OperationNode>
}

type GroupByRollupNodeFactory = Readonly<{
  is(node: OperationNode): node is GroupByRollupNode
  create(columns: ReadonlyArray<OperationNode>): Readonly<GroupByRollupNode>
}>

/**
 * @internal
 */
export const GroupByRollupNode: GroupByRollupNodeFactory =
  freeze<GroupByRollupNodeFactory>({
    is(node): node is GroupByRollupNode {
      return node.kind === 'GroupByRollupNode'
    },

    create(columns) {
      return freeze({
        kind: 'GroupByRollupNode',
        columns: freeze(columns),
      })
    },
  })

import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'

export type GroupingElementType = 'cube' | 'rollup' | 'grouping sets'

export interface GroupingElementNode extends OperationNode {
  readonly kind: 'GroupingElementNode'
  readonly type: GroupingElementType
  /**
   * For `cube` and `rollup` these are the grouped expressions. For
   * `grouping sets` each item is a `TupleNode` representing one set.
   */
  readonly items: ReadonlyArray<OperationNode>
}

type GroupingElementNodeFactory = Readonly<{
  is(node: OperationNode): node is GroupingElementNode
  create(
    type: GroupingElementType,
    items: ReadonlyArray<OperationNode>,
  ): Readonly<GroupingElementNode>
}>

/**
 * @internal
 */
export const GroupingElementNode: GroupingElementNodeFactory =
  freeze<GroupingElementNodeFactory>({
    is(node): node is GroupingElementNode {
      return node.kind === 'GroupingElementNode'
    },

    create(type, items) {
      return freeze({
        kind: 'GroupingElementNode',
        type,
        items: freeze(items),
      })
    },
  })

import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'

export type GroupByModifier = 'cube' | 'rollup' | 'grouping sets'

export interface GroupByModifierNode extends OperationNode {
  readonly kind: 'GroupByModifierNode'
  readonly modifier: GroupByModifier
  /**
   * For `cube` and `rollup`, a single flat list stored as one inner array.
   * For `grouping sets`, each inner array is one parenthesized set.
   */
  readonly sets: ReadonlyArray<ReadonlyArray<OperationNode>>
}

type GroupByModifierNodeFactory = Readonly<{
  is(node: OperationNode): node is GroupByModifierNode
  create(
    modifier: GroupByModifier,
    sets: ReadonlyArray<ReadonlyArray<OperationNode>>,
  ): Readonly<GroupByModifierNode>
}>

/**
 * @internal
 */
export const GroupByModifierNode: GroupByModifierNodeFactory =
  freeze<GroupByModifierNodeFactory>({
    is(node): node is GroupByModifierNode {
      return node.kind === 'GroupByModifierNode'
    },

    create(modifier, sets) {
      return freeze({
        kind: 'GroupByModifierNode',
        modifier,
        sets: freeze(sets.map((set) => freeze([...set]))),
      })
    },
  })

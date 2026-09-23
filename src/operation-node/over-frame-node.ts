import { freeze } from '../util/object-utils.js'
import type { FrameBoundNode } from './frame-bound-node.js'
import type { OperationNode } from './operation-node.js'

export type OverFrameUnit = 'rows' | 'range' | 'groups'

export type OverFrameExclusion = 'current row' | 'group' | 'ties' | 'no others'

export interface OverFrameNode extends OperationNode {
  readonly kind: 'OverFrameNode'
  readonly unit: OverFrameUnit
  readonly start: FrameBoundNode
  readonly end?: FrameBoundNode
  readonly exclusion?: OverFrameExclusion
}

type OverFrameNodeFactory = Readonly<{
  is(node: OperationNode): node is OverFrameNode
  create(
    unit: OverFrameUnit,
    start: FrameBoundNode,
    end?: FrameBoundNode,
    exclusion?: OverFrameExclusion,
  ): Readonly<OverFrameNode>
}>

/**
 * @internal
 */
export const OverFrameNode: OverFrameNodeFactory = freeze<OverFrameNodeFactory>(
  {
    is(node): node is OverFrameNode {
      return node.kind === 'OverFrameNode'
    },

    create(unit, start, end, exclusion) {
      return freeze({
        kind: 'OverFrameNode',
        unit,
        start,
        end,
        exclusion,
      })
    },
  },
)

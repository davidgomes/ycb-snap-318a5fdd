import { freeze } from '../util/object-utils.js'
import type { FrameBoundNode } from './frame-bound-node.js'
import type { OperationNode } from './operation-node.js'

export type FrameMode = 'rows' | 'range' | 'groups'

export type FrameExclusion = 'current row' | 'group' | 'ties' | 'no others'

export interface FrameNode extends OperationNode {
  readonly kind: 'FrameNode'
  readonly mode: FrameMode
  readonly start: FrameBoundNode
  readonly end?: FrameBoundNode
  readonly exclusion?: FrameExclusion
}

type FrameNodeFactory = Readonly<{
  is(node: OperationNode): node is FrameNode
  create(
    mode: FrameMode,
    start: FrameBoundNode,
    end?: FrameBoundNode,
    exclusion?: FrameExclusion,
  ): Readonly<FrameNode>
}>

/**
 * @internal
 */
export const FrameNode: FrameNodeFactory = freeze<FrameNodeFactory>({
  is(node): node is FrameNode {
    return node.kind === 'FrameNode'
  },

  create(mode, start, end, exclusion) {
    return freeze({
      kind: 'FrameNode',
      mode,
      start,
      ...(end ? { end } : {}),
      ...(exclusion ? { exclusion } : {}),
    })
  },
})

import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'

export type FrameMode = 'rows' | 'range' | 'groups'

export type FrameBoundType =
  | 'unbounded preceding'
  | 'preceding'
  | 'current row'
  | 'following'
  | 'unbounded following'

export type FrameExclusion = 'current row' | 'group' | 'ties' | 'no others'

export interface FrameBound {
  readonly type: FrameBoundType
  readonly offset?: OperationNode
}

export interface FrameNode extends OperationNode {
  readonly kind: 'FrameNode'
  readonly mode: FrameMode
  readonly start: FrameBound
  readonly end?: FrameBound
  readonly exclude?: FrameExclusion
}

type FrameNodeFactory = Readonly<{
  is(node: OperationNode): node is FrameNode
  create(
    mode: FrameMode,
    start: FrameBound,
    end?: FrameBound,
  ): Readonly<FrameNode>
  cloneWithExclusion(
    frameNode: FrameNode,
    exclude: FrameExclusion,
  ): Readonly<FrameNode>
}>

/**
 * @internal
 */
export const FrameNode: FrameNodeFactory = freeze<FrameNodeFactory>({
  is(node): node is FrameNode {
    return node.kind === 'FrameNode'
  },

  create(mode, start, end?) {
    return freeze({
      kind: 'FrameNode',
      mode,
      start: freeze(start),
      ...(end ? { end: freeze(end) } : {}),
    })
  },

  cloneWithExclusion(frameNode, exclude) {
    return freeze({
      ...frameNode,
      exclude,
    })
  },
})

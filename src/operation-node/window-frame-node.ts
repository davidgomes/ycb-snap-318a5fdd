import { freeze } from '../util/object-utils.js'
import type { OperationNode } from './operation-node.js'
import type { WindowFrameBoundNode } from './window-frame-bound-node.js'

export type WindowFrameMode = 'rows' | 'range' | 'groups'

export type WindowFrameExclusion =
  | 'current row'
  | 'group'
  | 'ties'
  | 'no others'

export interface WindowFrameNode extends OperationNode {
  readonly kind: 'WindowFrameNode'
  readonly mode: WindowFrameMode
  readonly start: WindowFrameBoundNode
  readonly end?: WindowFrameBoundNode
  readonly exclusion?: WindowFrameExclusion
}

type WindowFrameNodeFactory = Readonly<{
  is(node: OperationNode): node is WindowFrameNode
  create(props: {
    mode: WindowFrameMode
    start: WindowFrameBoundNode
    end?: WindowFrameBoundNode
    exclusion?: WindowFrameExclusion
  }): Readonly<WindowFrameNode>
}>

/**
 * @internal
 */
export const WindowFrameNode: WindowFrameNodeFactory =
  freeze<WindowFrameNodeFactory>({
    is(node): node is WindowFrameNode {
      return node.kind === 'WindowFrameNode'
    },

    create({ mode, start, end, exclusion }) {
      return freeze({
        kind: 'WindowFrameNode',
        mode,
        start,
        ...(end && { end }),
        ...(exclusion && { exclusion }),
      })
    },
  })

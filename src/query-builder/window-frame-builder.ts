import type { Expression } from '../expression/expression.js'
import type { OperationNodeSource } from '../operation-node/operation-node-source.js'
import {
  WindowFrameBoundNode,
  type WindowFrameBoundType,
} from '../operation-node/window-frame-bound-node.js'
import {
  WindowFrameNode,
  type WindowFrameExclusion,
  type WindowFrameMode,
} from '../operation-node/window-frame-node.js'
import { ValueNode } from '../operation-node/value-node.js'
import { freeze } from '../util/object-utils.js'

export type FrameOffset = number | bigint | Expression<any>

export interface WindowFrameBuilderProps {
  readonly mode: WindowFrameMode
  readonly start?: WindowFrameBoundNode
  readonly end?: WindowFrameBoundNode
  readonly exclusion?: WindowFrameExclusion
}

function parseFrameOffset(offset: FrameOffset) {
  if (typeof offset === 'number' || typeof offset === 'bigint') {
    return ValueNode.create(offset)
  }

  return offset.toOperationNode()
}

export class WindowFrameBuilder implements OperationNodeSource {
  readonly #props: WindowFrameBuilderProps

  constructor(props: WindowFrameBuilderProps) {
    this.#props = freeze(props)
  }

  unboundedPreceding(): WindowFrameBuilder {
    return this.#withStart('unbounded preceding')
  }

  preceding(offset: FrameOffset): WindowFrameBuilder {
    return this.#withStart('preceding', offset)
  }

  currentRow(): WindowFrameBuilder {
    return this.#withStart('current row')
  }

  following(offset: FrameOffset): WindowFrameBuilder {
    return this.#withStart('following', offset)
  }

  unboundedFollowing(): WindowFrameBuilder {
    return this.#withStart('unbounded following')
  }

  betweenUnboundedPreceding(): WindowFrameBuilder {
    return this.#withStart('unbounded preceding')
  }

  betweenPreceding(offset: FrameOffset): WindowFrameBuilder {
    return this.#withStart('preceding', offset)
  }

  betweenCurrentRow(): WindowFrameBuilder {
    return this.#withStart('current row')
  }

  betweenFollowing(offset: FrameOffset): WindowFrameBuilder {
    return this.#withStart('following', offset)
  }

  andUnboundedPreceding(): WindowFrameBuilder {
    return this.#withEnd('unbounded preceding')
  }

  andPreceding(offset: FrameOffset): WindowFrameBuilder {
    return this.#withEnd('preceding', offset)
  }

  andCurrentRow(): WindowFrameBuilder {
    return this.#withEnd('current row')
  }

  andFollowing(offset: FrameOffset): WindowFrameBuilder {
    return this.#withEnd('following', offset)
  }

  andUnboundedFollowing(): WindowFrameBuilder {
    return this.#withEnd('unbounded following')
  }

  excludeCurrentRow(): WindowFrameBuilder {
    return this.#withExclusion('current row')
  }

  excludeGroup(): WindowFrameBuilder {
    return this.#withExclusion('group')
  }

  excludeTies(): WindowFrameBuilder {
    return this.#withExclusion('ties')
  }

  excludeNoOthers(): WindowFrameBuilder {
    return this.#withExclusion('no others')
  }

  toOperationNode(): WindowFrameNode {
    if (!this.#props.start) {
      throw new Error('window frame must specify at least a start bound')
    }

    return WindowFrameNode.create({
      mode: this.#props.mode,
      start: this.#props.start,
      end: this.#props.end,
      exclusion: this.#props.exclusion,
    })
  }

  #withStart(
    boundType: WindowFrameBoundType,
    offset?: FrameOffset,
  ): WindowFrameBuilder {
    return new WindowFrameBuilder({
      ...this.#props,
      start: WindowFrameBoundNode.create(
        boundType,
        offset !== undefined ? parseFrameOffset(offset) : undefined,
      ),
    })
  }

  #withEnd(
    boundType: WindowFrameBoundType,
    offset?: FrameOffset,
  ): WindowFrameBuilder {
    return new WindowFrameBuilder({
      ...this.#props,
      end: WindowFrameBoundNode.create(
        boundType,
        offset !== undefined ? parseFrameOffset(offset) : undefined,
      ),
    })
  }

  #withExclusion(exclusion: WindowFrameExclusion): WindowFrameBuilder {
    return new WindowFrameBuilder({
      ...this.#props,
      exclusion,
    })
  }
}

export function createWindowFrameBuilder(
  mode: WindowFrameMode,
): WindowFrameBuilder {
  return new WindowFrameBuilder({ mode })
}

export type WindowFrameBuilderCallback = (
  builder: WindowFrameBuilder,
) => WindowFrameBuilder

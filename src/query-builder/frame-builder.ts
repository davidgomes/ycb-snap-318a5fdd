import type { Expression } from '../expression/expression.js'
import {
  FrameBoundNode,
  type FrameBoundType,
} from '../operation-node/frame-bound-node.js'
import {
  FrameNode,
  type FrameExclusion,
  type FrameMode,
} from '../operation-node/frame-node.js'
import type { OperationNode } from '../operation-node/operation-node.js'
import type { OperationNodeSource } from '../operation-node/operation-node-source.js'
import { parseExpression } from '../parser/expression-parser.js'
import { ValueNode } from '../operation-node/value-node.js'
import { freeze, isBigInt, isNumber } from '../util/object-utils.js'

/**
 * A numeric window-frame offset, or an expression compiled inline.
 */
export type FrameOffset = number | bigint | Expression<any>

/**
 * Starts a window frame bound. Single-bound methods finish the frame.
 * `between*` methods must be completed with an `and*` method.
 */
export interface FrameStartBuilder {
  unboundedPreceding(): FrameExtentBuilder
  preceding(offset: FrameOffset): FrameExtentBuilder
  currentRow(): FrameExtentBuilder
  following(offset: FrameOffset): FrameExtentBuilder
  unboundedFollowing(): FrameExtentBuilder

  betweenUnboundedPreceding(): FrameBetweenBuilder
  betweenPreceding(offset: FrameOffset): FrameBetweenBuilder
  betweenCurrentRow(): FrameBetweenBuilder
  betweenFollowing(offset: FrameOffset): FrameBetweenBuilder
}

/**
 * Completes a `between` frame bound.
 */
export interface FrameBetweenBuilder {
  andUnboundedPreceding(): FrameExtentBuilder
  andPreceding(offset: FrameOffset): FrameExtentBuilder
  andCurrentRow(): FrameExtentBuilder
  andFollowing(offset: FrameOffset): FrameExtentBuilder
  andUnboundedFollowing(): FrameExtentBuilder
}

/**
 * A finished window frame. Exclusion modifiers can still be applied.
 */
export interface FrameExtentBuilder extends OperationNodeSource {
  excludeCurrentRow(): FrameExtentBuilder
  excludeGroup(): FrameExtentBuilder
  excludeTies(): FrameExtentBuilder
  excludeNoOthers(): FrameExtentBuilder
  toOperationNode(): FrameNode
}

export function parseFrameOffset(offset: FrameOffset): OperationNode {
  if (isNumber(offset) || isBigInt(offset)) {
    return ValueNode.create(offset)
  }

  return parseExpression(offset)
}

interface FrameBuilderProps {
  readonly mode: FrameMode
  readonly start?: FrameBoundNode
  readonly end?: FrameBoundNode
  readonly exclusion?: FrameExclusion
}

export class FrameBuilder
  implements FrameStartBuilder, FrameBetweenBuilder, FrameExtentBuilder
{
  readonly #props: FrameBuilderProps

  constructor(props: FrameBuilderProps) {
    this.#props = freeze(props)
  }

  unboundedPreceding(): FrameExtentBuilder {
    return this.#withStart(bound('unbounded preceding'))
  }

  preceding(offset: FrameOffset): FrameExtentBuilder {
    return this.#withStart(bound('preceding', offset))
  }

  currentRow(): FrameExtentBuilder {
    return this.#withStart(bound('current row'))
  }

  following(offset: FrameOffset): FrameExtentBuilder {
    return this.#withStart(bound('following', offset))
  }

  unboundedFollowing(): FrameExtentBuilder {
    return this.#withStart(bound('unbounded following'))
  }

  betweenUnboundedPreceding(): FrameBetweenBuilder {
    return this.#withStart(bound('unbounded preceding'))
  }

  betweenPreceding(offset: FrameOffset): FrameBetweenBuilder {
    return this.#withStart(bound('preceding', offset))
  }

  betweenCurrentRow(): FrameBetweenBuilder {
    return this.#withStart(bound('current row'))
  }

  betweenFollowing(offset: FrameOffset): FrameBetweenBuilder {
    return this.#withStart(bound('following', offset))
  }

  andUnboundedPreceding(): FrameExtentBuilder {
    return this.#withEnd(bound('unbounded preceding'))
  }

  andPreceding(offset: FrameOffset): FrameExtentBuilder {
    return this.#withEnd(bound('preceding', offset))
  }

  andCurrentRow(): FrameExtentBuilder {
    return this.#withEnd(bound('current row'))
  }

  andFollowing(offset: FrameOffset): FrameExtentBuilder {
    return this.#withEnd(bound('following', offset))
  }

  andUnboundedFollowing(): FrameExtentBuilder {
    return this.#withEnd(bound('unbounded following'))
  }

  excludeCurrentRow(): FrameExtentBuilder {
    return this.#withExclusion('current row')
  }

  excludeGroup(): FrameExtentBuilder {
    return this.#withExclusion('group')
  }

  excludeTies(): FrameExtentBuilder {
    return this.#withExclusion('ties')
  }

  excludeNoOthers(): FrameExtentBuilder {
    return this.#withExclusion('no others')
  }

  toOperationNode(): FrameNode {
    if (!this.#props.start) {
      throw new Error('incomplete window frame')
    }

    return FrameNode.create(
      this.#props.mode,
      this.#props.start,
      this.#props.end,
      this.#props.exclusion,
    )
  }

  #withStart(start: FrameBoundNode): FrameBuilder {
    return new FrameBuilder({
      ...this.#props,
      start,
    })
  }

  #withEnd(end: FrameBoundNode): FrameBuilder {
    return new FrameBuilder({
      ...this.#props,
      end,
    })
  }

  #withExclusion(exclusion: FrameExclusion): FrameBuilder {
    return new FrameBuilder({
      ...this.#props,
      exclusion,
    })
  }
}

function bound(type: FrameBoundType, offset?: FrameOffset): FrameBoundNode {
  return FrameBoundNode.create(
    type,
    offset === undefined ? undefined : parseFrameOffset(offset),
  )
}

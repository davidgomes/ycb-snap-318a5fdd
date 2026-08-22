import type { Expression } from '../expression/expression.js'
import { FrameBoundNode } from '../operation-node/frame-bound-node.js'
import {
  FrameNode,
  type FrameExclusion,
  type FrameMode,
} from '../operation-node/frame-node.js'
import type { OperationNode } from '../operation-node/operation-node.js'
import type { OperationNodeSource } from '../operation-node/operation-node-source.js'
import { ValueNode } from '../operation-node/value-node.js'
import { freeze, isBigInt, isNumber } from '../util/object-utils.js'

/**
 * A numeric frame offset or an inline SQL expression.
 *
 * Plain `number` and `bigint` values are compiled as query parameters.
 * {@link Expression} values are compiled as-is so callers can emit SQL literals.
 */
export type FrameOffset = number | bigint | Expression<any>

export class FrameBuilder implements OperationNodeSource {
  readonly #props: FrameBuilderProps

  constructor(props: FrameBuilderProps) {
    this.#props = freeze(props)
  }

  unboundedPreceding(): FrameBuilder {
    return this.#withStart(FrameBoundNode.create('unbounded preceding'))
  }

  preceding(offset: FrameOffset): FrameBuilder {
    return this.#withStart(
      FrameBoundNode.create('preceding', parseFrameOffset(offset)),
    )
  }

  currentRow(): FrameBuilder {
    return this.#withStart(FrameBoundNode.create('current row'))
  }

  following(offset: FrameOffset): FrameBuilder {
    return this.#withStart(
      FrameBoundNode.create('following', parseFrameOffset(offset)),
    )
  }

  unboundedFollowing(): FrameBuilder {
    return this.#withStart(FrameBoundNode.create('unbounded following'))
  }

  betweenUnboundedPreceding(): FrameBuilder {
    return this.#withStart(FrameBoundNode.create('unbounded preceding'), true)
  }

  betweenPreceding(offset: FrameOffset): FrameBuilder {
    return this.#withStart(
      FrameBoundNode.create('preceding', parseFrameOffset(offset)),
      true,
    )
  }

  betweenCurrentRow(): FrameBuilder {
    return this.#withStart(FrameBoundNode.create('current row'), true)
  }

  betweenFollowing(offset: FrameOffset): FrameBuilder {
    return this.#withStart(
      FrameBoundNode.create('following', parseFrameOffset(offset)),
      true,
    )
  }

  andUnboundedPreceding(): FrameBuilder {
    return this.#withEnd(FrameBoundNode.create('unbounded preceding'))
  }

  andPreceding(offset: FrameOffset): FrameBuilder {
    return this.#withEnd(
      FrameBoundNode.create('preceding', parseFrameOffset(offset)),
    )
  }

  andCurrentRow(): FrameBuilder {
    return this.#withEnd(FrameBoundNode.create('current row'))
  }

  andFollowing(offset: FrameOffset): FrameBuilder {
    return this.#withEnd(
      FrameBoundNode.create('following', parseFrameOffset(offset)),
    )
  }

  andUnboundedFollowing(): FrameBuilder {
    return this.#withEnd(FrameBoundNode.create('unbounded following'))
  }

  excludeCurrentRow(): FrameBuilder {
    return this.#withExclusion('current row')
  }

  excludeGroup(): FrameBuilder {
    return this.#withExclusion('group')
  }

  excludeTies(): FrameBuilder {
    return this.#withExclusion('ties')
  }

  excludeNoOthers(): FrameBuilder {
    return this.#withExclusion('no others')
  }

  toOperationNode(): FrameNode {
    return FrameNode.create(
      this.#props.mode,
      this.#props.start ?? FrameBoundNode.create('unbounded preceding'),
      this.#props.end,
      this.#props.exclusion,
    )
  }

  #withStart(start: FrameBoundNode, between = false): FrameBuilder {
    return new FrameBuilder({
      ...this.#props,
      start,
      end: between ? this.#props.end : undefined,
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

export interface FrameBuilderProps {
  readonly mode: FrameMode
  readonly start?: FrameBoundNode
  readonly end?: FrameBoundNode
  readonly exclusion?: FrameExclusion
}

export function createFrameBuilder(mode: FrameMode): FrameBuilder {
  return new FrameBuilder({ mode })
}

export function parseFrameOffset(offset: FrameOffset): OperationNode {
  if (isNumber(offset) || isBigInt(offset)) {
    return ValueNode.create(offset)
  }

  return offset.toOperationNode()
}

export type FrameBuilderCallback = (builder: FrameBuilder) => FrameBuilder

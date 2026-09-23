import type { Expression } from '../expression/expression.js'
import {
  type FrameBound,
  type FrameBoundType,
  type FrameExclusion,
  type FrameMode,
  FrameNode,
} from '../operation-node/frame-node.js'
import type { OperationNode } from '../operation-node/operation-node.js'
import type { OperationNodeSource } from '../operation-node/operation-node-source.js'
import { ValueNode } from '../operation-node/value-node.js'
import { freeze } from '../util/object-utils.js'

export type FrameOffset = number | bigint | Expression<any>

function parseFrameOffset(offset: FrameOffset): OperationNode {
  return typeof offset === 'number' || typeof offset === 'bigint'
    ? ValueNode.create(offset)
    : offset.toOperationNode()
}

function bound(type: FrameBoundType, offset?: FrameOffset): FrameBound {
  return offset === undefined
    ? { type }
    : { type, offset: parseFrameOffset(offset) }
}

/**
 * Starting point of a window frame specification created by
 * {@link OverBuilder.rows}, {@link OverBuilder.range} or {@link OverBuilder.groups}.
 */
export class FrameBuilder {
  readonly #mode: FrameMode

  constructor(mode: FrameMode) {
    this.#mode = mode
  }

  unboundedPreceding(): FrameExclusionBuilder {
    return this.#single(bound('unbounded preceding'))
  }

  preceding(offset: FrameOffset): FrameExclusionBuilder {
    return this.#single(bound('preceding', offset))
  }

  currentRow(): FrameExclusionBuilder {
    return this.#single(bound('current row'))
  }

  following(offset: FrameOffset): FrameExclusionBuilder {
    return this.#single(bound('following', offset))
  }

  unboundedFollowing(): FrameExclusionBuilder {
    return this.#single(bound('unbounded following'))
  }

  betweenUnboundedPreceding(): FrameBetweenBuilder {
    return new FrameBetweenBuilder(this.#mode, bound('unbounded preceding'))
  }

  betweenPreceding(offset: FrameOffset): FrameBetweenBuilder {
    return new FrameBetweenBuilder(this.#mode, bound('preceding', offset))
  }

  betweenCurrentRow(): FrameBetweenBuilder {
    return new FrameBetweenBuilder(this.#mode, bound('current row'))
  }

  betweenFollowing(offset: FrameOffset): FrameBetweenBuilder {
    return new FrameBetweenBuilder(this.#mode, bound('following', offset))
  }

  #single(start: FrameBound): FrameExclusionBuilder {
    return new FrameExclusionBuilder(FrameNode.create(this.#mode, start))
  }
}

/**
 * Completes a `between ... and ...` window frame specification.
 */
export class FrameBetweenBuilder {
  readonly #mode: FrameMode
  readonly #start: FrameBound

  constructor(mode: FrameMode, start: FrameBound) {
    this.#mode = mode
    this.#start = freeze(start)
  }

  andUnboundedPreceding(): FrameExclusionBuilder {
    return this.#end(bound('unbounded preceding'))
  }

  andPreceding(offset: FrameOffset): FrameExclusionBuilder {
    return this.#end(bound('preceding', offset))
  }

  andCurrentRow(): FrameExclusionBuilder {
    return this.#end(bound('current row'))
  }

  andFollowing(offset: FrameOffset): FrameExclusionBuilder {
    return this.#end(bound('following', offset))
  }

  andUnboundedFollowing(): FrameExclusionBuilder {
    return this.#end(bound('unbounded following'))
  }

  #end(end: FrameBound): FrameExclusionBuilder {
    return new FrameExclusionBuilder(
      FrameNode.create(this.#mode, this.#start, end),
    )
  }
}

/**
 * A complete window frame specification that can optionally be given an
 * `exclude` clause.
 */
export class FrameExclusionBuilder implements OperationNodeSource {
  readonly #node: FrameNode

  constructor(node: FrameNode) {
    this.#node = node
  }

  excludeCurrentRow(): FrameExclusionBuilder {
    return this.#exclude('current row')
  }

  excludeGroup(): FrameExclusionBuilder {
    return this.#exclude('group')
  }

  excludeTies(): FrameExclusionBuilder {
    return this.#exclude('ties')
  }

  excludeNoOthers(): FrameExclusionBuilder {
    return this.#exclude('no others')
  }

  #exclude(exclusion: FrameExclusion): FrameExclusionBuilder {
    return new FrameExclusionBuilder(
      FrameNode.cloneWithExclusion(this.#node, exclusion),
    )
  }

  toOperationNode(): FrameNode {
    return this.#node
  }
}

export type FrameBuilderCallback = (fb: FrameBuilder) => FrameExclusionBuilder

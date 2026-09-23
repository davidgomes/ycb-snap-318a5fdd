import type { Expression } from '../expression/expression.js'
import {
  FrameBoundNode,
  type FrameBoundType,
} from '../operation-node/frame-bound-node.js'
import {
  type FrameExclusion,
  type FrameMode,
  FrameNode,
} from '../operation-node/frame-node.js'
import type { OperationNode } from '../operation-node/operation-node.js'
import {
  isOperationNodeSource,
  type OperationNodeSource,
} from '../operation-node/operation-node-source.js'
import { ValueNode } from '../operation-node/value-node.js'
import { freeze } from '../util/object-utils.js'

/**
 * An offset of a `preceding` or `following` frame bound.
 *
 * Numbers and bigints are passed as parameters. Use an expression such as
 * `sql.lit(3)` or `sql\`interval '1 day'\`` to inline the offset in the SQL.
 */
export type FrameOffset = number | bigint | Expression<any>

/**
 * Builds the frame extent of an `over` clause. Passed to the callbacks of
 * {@link OverBuilder.rows}, {@link OverBuilder.range} and {@link OverBuilder.groups}.
 */
export class FrameBuilder {
  readonly #mode: FrameMode

  constructor(mode: FrameMode) {
    this.#mode = mode
  }

  /**
   * `unbounded preceding`
   */
  unboundedPreceding(): FrameClauseBuilder {
    return this.#single(FrameBoundNode.create('unbounded preceding'))
  }

  /**
   * `<offset> preceding`
   */
  preceding(offset: FrameOffset): FrameClauseBuilder {
    return this.#single(parseFrameBound('preceding', offset))
  }

  /**
   * `current row`
   */
  currentRow(): FrameClauseBuilder {
    return this.#single(FrameBoundNode.create('current row'))
  }

  /**
   * `<offset> following`
   */
  following(offset: FrameOffset): FrameClauseBuilder {
    return this.#single(parseFrameBound('following', offset))
  }

  /**
   * `unbounded following`
   */
  unboundedFollowing(): FrameClauseBuilder {
    return this.#single(FrameBoundNode.create('unbounded following'))
  }

  /**
   * `between unbounded preceding and ...`
   */
  betweenUnboundedPreceding(): FrameBetweenBuilder {
    return this.#between(FrameBoundNode.create('unbounded preceding'))
  }

  /**
   * `between <offset> preceding and ...`
   */
  betweenPreceding(offset: FrameOffset): FrameBetweenBuilder {
    return this.#between(parseFrameBound('preceding', offset))
  }

  /**
   * `between current row and ...`
   */
  betweenCurrentRow(): FrameBetweenBuilder {
    return this.#between(FrameBoundNode.create('current row'))
  }

  /**
   * `between <offset> following and ...`
   */
  betweenFollowing(offset: FrameOffset): FrameBetweenBuilder {
    return this.#between(parseFrameBound('following', offset))
  }

  #single(start: FrameBoundNode): FrameClauseBuilder {
    return new FrameClauseBuilder(FrameNode.create(this.#mode, start))
  }

  #between(start: FrameBoundNode): FrameBetweenBuilder {
    return new FrameBetweenBuilder({ mode: this.#mode, start })
  }
}

/**
 * The second half of a `between ... and ...` frame extent.
 */
export class FrameBetweenBuilder {
  readonly #props: FrameBetweenBuilderProps

  constructor(props: FrameBetweenBuilderProps) {
    this.#props = freeze(props)
  }

  /**
   * `and unbounded preceding`
   */
  andUnboundedPreceding(): FrameClauseBuilder {
    return this.#and(FrameBoundNode.create('unbounded preceding'))
  }

  /**
   * `and <offset> preceding`
   */
  andPreceding(offset: FrameOffset): FrameClauseBuilder {
    return this.#and(parseFrameBound('preceding', offset))
  }

  /**
   * `and current row`
   */
  andCurrentRow(): FrameClauseBuilder {
    return this.#and(FrameBoundNode.create('current row'))
  }

  /**
   * `and <offset> following`
   */
  andFollowing(offset: FrameOffset): FrameClauseBuilder {
    return this.#and(parseFrameBound('following', offset))
  }

  /**
   * `and unbounded following`
   */
  andUnboundedFollowing(): FrameClauseBuilder {
    return this.#and(FrameBoundNode.create('unbounded following'))
  }

  #and(end: FrameBoundNode): FrameClauseBuilder {
    return new FrameClauseBuilder(
      FrameNode.create(this.#props.mode, this.#props.start, end),
    )
  }
}

/**
 * A complete frame extent that can optionally be given an exclusion clause.
 */
export class FrameClauseBuilder implements OperationNodeSource {
  readonly #frameNode: FrameNode

  constructor(frameNode: FrameNode) {
    this.#frameNode = frameNode
  }

  /**
   * `exclude current row`
   */
  excludeCurrentRow(): FrameClauseBuilder {
    return this.#exclude('current row')
  }

  /**
   * `exclude group`
   */
  excludeGroup(): FrameClauseBuilder {
    return this.#exclude('group')
  }

  /**
   * `exclude ties`
   */
  excludeTies(): FrameClauseBuilder {
    return this.#exclude('ties')
  }

  /**
   * `exclude no others`
   */
  excludeNoOthers(): FrameClauseBuilder {
    return this.#exclude('no others')
  }

  toOperationNode(): FrameNode {
    return this.#frameNode
  }

  #exclude(exclusion: FrameExclusion): FrameClauseBuilder {
    return new FrameClauseBuilder(
      FrameNode.cloneWithExclusion(this.#frameNode, exclusion),
    )
  }
}

export interface FrameBetweenBuilderProps {
  readonly mode: FrameMode
  readonly start: FrameBoundNode
}

export type FrameBuilderCallback = (builder: FrameBuilder) => FrameClauseBuilder

function parseFrameBound(
  type: Extract<FrameBoundType, 'preceding' | 'following'>,
  offset: FrameOffset,
): FrameBoundNode {
  return FrameBoundNode.create(type, parseFrameOffset(offset))
}

function parseFrameOffset(offset: FrameOffset): OperationNode {
  return isOperationNodeSource(offset)
    ? offset.toOperationNode()
    : ValueNode.create(offset)
}

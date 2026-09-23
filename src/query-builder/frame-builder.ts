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
import {
  isOperationNodeSource,
  type OperationNodeSource,
} from '../operation-node/operation-node-source.js'
import type { OperationNode } from '../operation-node/operation-node.js'
import { ValueNode } from '../operation-node/value-node.js'
import { freeze } from '../util/object-utils.js'

/**
 * The offset of a `preceding` or `following` frame bound.
 *
 * Numbers and bigints are passed as parameters. Use an expression such as
 * `sql.lit(3)` to inline the offset in the SQL.
 */
export type FrameOffsetExpression = number | bigint | Expression<any>

/**
 * Builds the frame clause (`rows`, `range` or `groups`) of an `over` clause.
 *
 * An instance of this builder is passed to the callbacks of
 * {@link OverBuilder.rows}, {@link OverBuilder.range} and {@link OverBuilder.groups}.
 */
export class FrameBuilder {
  readonly #props: FrameBuilderProps

  constructor(props: FrameBuilderProps) {
    this.#props = freeze(props)
  }

  /**
   * Single bound `unbounded preceding` frame.
   */
  unboundedPreceding(): FrameExclusionBuilder {
    return this.#single('unbounded preceding')
  }

  /**
   * Single bound `<offset> preceding` frame.
   */
  preceding(offset: FrameOffsetExpression): FrameExclusionBuilder {
    return this.#single('preceding', offset)
  }

  /**
   * Single bound `current row` frame.
   */
  currentRow(): FrameExclusionBuilder {
    return this.#single('current row')
  }

  /**
   * Single bound `<offset> following` frame.
   */
  following(offset: FrameOffsetExpression): FrameExclusionBuilder {
    return this.#single('following', offset)
  }

  /**
   * Single bound `unbounded following` frame.
   */
  unboundedFollowing(): FrameExclusionBuilder {
    return this.#single('unbounded following')
  }

  /**
   * Starts a `between unbounded preceding and ...` frame.
   */
  betweenUnboundedPreceding(): FrameBetweenBuilder {
    return this.#between('unbounded preceding')
  }

  /**
   * Starts a `between <offset> preceding and ...` frame.
   */
  betweenPreceding(offset: FrameOffsetExpression): FrameBetweenBuilder {
    return this.#between('preceding', offset)
  }

  /**
   * Starts a `between current row and ...` frame.
   */
  betweenCurrentRow(): FrameBetweenBuilder {
    return this.#between('current row')
  }

  /**
   * Starts a `between <offset> following and ...` frame.
   */
  betweenFollowing(offset: FrameOffsetExpression): FrameBetweenBuilder {
    return this.#between('following', offset)
  }

  #single(
    type: FrameBoundType,
    offset?: FrameOffsetExpression,
  ): FrameExclusionBuilder {
    return new FrameExclusionBuilder({
      frameNode: FrameNode.create(
        this.#props.mode,
        parseFrameBound(type, offset),
      ),
    })
  }

  #between(
    type: FrameBoundType,
    offset?: FrameOffsetExpression,
  ): FrameBetweenBuilder {
    return new FrameBetweenBuilder({
      frameNode: FrameNode.create(
        this.#props.mode,
        parseFrameBound(type, offset),
      ),
    })
  }
}

export interface FrameBuilderProps {
  readonly mode: FrameMode
}

/**
 * A frame started with one of the `between*` methods of {@link FrameBuilder}.
 * Must be completed with one of the `and*` methods.
 */
export class FrameBetweenBuilder {
  readonly #props: FrameNodeBuilderProps

  constructor(props: FrameNodeBuilderProps) {
    this.#props = freeze(props)
  }

  /**
   * Ends the frame with `and unbounded preceding`.
   */
  andUnboundedPreceding(): FrameExclusionBuilder {
    return this.#and('unbounded preceding')
  }

  /**
   * Ends the frame with `and <offset> preceding`.
   */
  andPreceding(offset: FrameOffsetExpression): FrameExclusionBuilder {
    return this.#and('preceding', offset)
  }

  /**
   * Ends the frame with `and current row`.
   */
  andCurrentRow(): FrameExclusionBuilder {
    return this.#and('current row')
  }

  /**
   * Ends the frame with `and <offset> following`.
   */
  andFollowing(offset: FrameOffsetExpression): FrameExclusionBuilder {
    return this.#and('following', offset)
  }

  /**
   * Ends the frame with `and unbounded following`.
   */
  andUnboundedFollowing(): FrameExclusionBuilder {
    return this.#and('unbounded following')
  }

  #and(
    type: FrameBoundType,
    offset?: FrameOffsetExpression,
  ): FrameExclusionBuilder {
    return new FrameExclusionBuilder({
      frameNode: FrameNode.cloneWithEnd(
        this.#props.frameNode,
        parseFrameBound(type, offset),
      ),
    })
  }
}

/**
 * A complete frame. Optionally accepts an `exclude` clause.
 */
export class FrameExclusionBuilder implements OperationNodeSource {
  readonly #props: FrameNodeBuilderProps

  constructor(props: FrameNodeBuilderProps) {
    this.#props = freeze(props)
  }

  /**
   * Adds an `exclude current row` clause to the frame.
   */
  excludeCurrentRow(): FrameExclusionBuilder {
    return this.#exclude('current row')
  }

  /**
   * Adds an `exclude group` clause to the frame.
   */
  excludeGroup(): FrameExclusionBuilder {
    return this.#exclude('group')
  }

  /**
   * Adds an `exclude ties` clause to the frame.
   */
  excludeTies(): FrameExclusionBuilder {
    return this.#exclude('ties')
  }

  /**
   * Adds an `exclude no others` clause to the frame.
   */
  excludeNoOthers(): FrameExclusionBuilder {
    return this.#exclude('no others')
  }

  toOperationNode(): FrameNode {
    return this.#props.frameNode
  }

  #exclude(exclusion: FrameExclusion): FrameExclusionBuilder {
    return new FrameExclusionBuilder({
      frameNode: FrameNode.cloneWithExclusion(this.#props.frameNode, exclusion),
    })
  }
}

export interface FrameNodeBuilderProps {
  readonly frameNode: FrameNode
}

export type FrameBuilderCallback = (
  builder: FrameBuilder,
) => FrameExclusionBuilder

function parseFrameBound(
  type: FrameBoundType,
  offset: FrameOffsetExpression | undefined,
): FrameBoundNode {
  return FrameBoundNode.create(
    type,
    offset === undefined ? undefined : parseFrameOffset(offset),
  )
}

function parseFrameOffset(offset: FrameOffsetExpression): OperationNode {
  return isOperationNodeSource(offset)
    ? offset.toOperationNode()
    : ValueNode.create(offset)
}

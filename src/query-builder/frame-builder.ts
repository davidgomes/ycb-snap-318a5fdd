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
import type { OperationNodeSource } from '../operation-node/operation-node-source.js'
import { parseValueExpression } from '../parser/value-parser.js'
import { freeze } from '../util/object-utils.js'

/**
 * A frame bound offset.
 *
 * Numbers and bigints are passed as parameters. Use an expression such as
 * `sql.lit(2)` to inline the offset in the SQL.
 */
export type FrameOffset = number | bigint | Expression<any>

/**
 * Starts a window frame definition. An instance of this builder is passed to
 * the callbacks of {@link OverBuilder.rows}, {@link OverBuilder.range} and
 * {@link OverBuilder.groups}.
 *
 * Use one of the single bound methods (e.g. {@link unboundedPreceding}) or
 * one of the `between*` methods followed by an `and*` method.
 */
export class FrameBuilder {
  readonly #props: FrameBuilderProps

  constructor(props: FrameBuilderProps) {
    this.#props = freeze(props)
  }

  /**
   * Frame starting at the first row of the partition: `unbounded preceding`.
   */
  unboundedPreceding(): FrameClauseBuilder {
    return this.#single(FrameBoundNode.create('unbounded preceding'))
  }

  /**
   * Frame starting `offset` rows, groups or values before the current row:
   * `<offset> preceding`.
   */
  preceding(offset: FrameOffset): FrameClauseBuilder {
    return this.#single(parseFrameBound('preceding', offset))
  }

  /**
   * Frame starting at the current row: `current row`.
   */
  currentRow(): FrameClauseBuilder {
    return this.#single(FrameBoundNode.create('current row'))
  }

  /**
   * Frame starting `offset` rows, groups or values after the current row:
   * `<offset> following`.
   */
  following(offset: FrameOffset): FrameClauseBuilder {
    return this.#single(parseFrameBound('following', offset))
  }

  /**
   * Frame starting after the last row of the partition: `unbounded following`.
   */
  unboundedFollowing(): FrameClauseBuilder {
    return this.#single(FrameBoundNode.create('unbounded following'))
  }

  /**
   * Starts a `between unbounded preceding and ...` frame.
   */
  betweenUnboundedPreceding(): FrameBetweenBuilder {
    return this.#between(FrameBoundNode.create('unbounded preceding'))
  }

  /**
   * Starts a `between <offset> preceding and ...` frame.
   */
  betweenPreceding(offset: FrameOffset): FrameBetweenBuilder {
    return this.#between(parseFrameBound('preceding', offset))
  }

  /**
   * Starts a `between current row and ...` frame.
   */
  betweenCurrentRow(): FrameBetweenBuilder {
    return this.#between(FrameBoundNode.create('current row'))
  }

  /**
   * Starts a `between <offset> following and ...` frame.
   */
  betweenFollowing(offset: FrameOffset): FrameBetweenBuilder {
    return this.#between(parseFrameBound('following', offset))
  }

  #single(start: FrameBoundNode): FrameClauseBuilder {
    return new FrameClauseBuilder({
      frameNode: FrameNode.create(this.#props.mode, start),
    })
  }

  #between(start: FrameBoundNode): FrameBetweenBuilder {
    return new FrameBetweenBuilder({ mode: this.#props.mode, start })
  }
}

/**
 * The second half of a `between ... and ...` window frame definition.
 */
export class FrameBetweenBuilder {
  readonly #props: FrameBetweenBuilderProps

  constructor(props: FrameBetweenBuilderProps) {
    this.#props = freeze(props)
  }

  /**
   * Ends the frame with `and unbounded preceding`.
   */
  andUnboundedPreceding(): FrameClauseBuilder {
    return this.#end(FrameBoundNode.create('unbounded preceding'))
  }

  /**
   * Ends the frame with `and <offset> preceding`.
   */
  andPreceding(offset: FrameOffset): FrameClauseBuilder {
    return this.#end(parseFrameBound('preceding', offset))
  }

  /**
   * Ends the frame with `and current row`.
   */
  andCurrentRow(): FrameClauseBuilder {
    return this.#end(FrameBoundNode.create('current row'))
  }

  /**
   * Ends the frame with `and <offset> following`.
   */
  andFollowing(offset: FrameOffset): FrameClauseBuilder {
    return this.#end(parseFrameBound('following', offset))
  }

  /**
   * Ends the frame with `and unbounded following`.
   */
  andUnboundedFollowing(): FrameClauseBuilder {
    return this.#end(FrameBoundNode.create('unbounded following'))
  }

  #end(end: FrameBoundNode): FrameClauseBuilder {
    return new FrameClauseBuilder({
      frameNode: FrameNode.create(this.#props.mode, this.#props.start, end),
    })
  }
}

/**
 * A complete window frame definition, optionally followed by a frame exclusion.
 */
export class FrameClauseBuilder implements OperationNodeSource {
  readonly #props: FrameClauseBuilderProps

  constructor(props: FrameClauseBuilderProps) {
    this.#props = freeze(props)
  }

  /**
   * Adds `exclude current row` to the frame.
   */
  excludeCurrentRow(): FrameClauseBuilder {
    return this.#exclude('current row')
  }

  /**
   * Adds `exclude group` to the frame, excluding the current row and its peers.
   */
  excludeGroup(): FrameClauseBuilder {
    return this.#exclude('group')
  }

  /**
   * Adds `exclude ties` to the frame, excluding the peers of the current row
   * but not the current row itself.
   */
  excludeTies(): FrameClauseBuilder {
    return this.#exclude('ties')
  }

  /**
   * Adds `exclude no others` to the frame.
   */
  excludeNoOthers(): FrameClauseBuilder {
    return this.#exclude('no others')
  }

  toOperationNode(): FrameNode {
    return this.#props.frameNode
  }

  #exclude(exclusion: FrameExclusion): FrameClauseBuilder {
    return new FrameClauseBuilder({
      frameNode: FrameNode.cloneWithExclusion(this.#props.frameNode, exclusion),
    })
  }
}

export type FrameBuilderCallback = (frame: FrameBuilder) => FrameClauseBuilder

export interface FrameBuilderProps {
  readonly mode: FrameMode
}

export interface FrameBetweenBuilderProps {
  readonly mode: FrameMode
  readonly start: FrameBoundNode
}

export interface FrameClauseBuilderProps {
  readonly frameNode: FrameNode
}

function parseFrameBound(
  type: Extract<FrameBoundType, 'preceding' | 'following'>,
  offset: FrameOffset,
): FrameBoundNode {
  return FrameBoundNode.create(type, parseValueExpression(offset))
}

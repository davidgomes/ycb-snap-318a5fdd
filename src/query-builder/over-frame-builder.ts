import { isExpression, type Expression } from '../expression/expression.js'
import {
  FrameBoundNode,
  type FrameBoundKind,
} from '../operation-node/frame-bound-node.js'
import {
  OverFrameNode,
  type OverFrameExclusion,
  type OverFrameUnit,
} from '../operation-node/over-frame-node.js'
import { ValueNode } from '../operation-node/value-node.js'
import { freeze } from '../util/object-utils.js'

export type FrameBoundOffset = number | bigint | Expression<any>

/**
 * Builds one side of an `over` frame (`rows` / `range` / `groups`).
 *
 * Single-bound methods finish the extent. `between*` methods must be
 * completed with an `and*` method.
 */
export class OverFrameBuilder {
  /**
   * `UNBOUNDED PRECEDING`.
   *
   * As a single bound this means "through the current row".
   */
  unboundedPreceding(): CompletedOverFrameBuilder {
    return new CompletedOverFrameBuilder({
      start: FrameBoundNode.create('unbounded preceding'),
    })
  }

  /**
   * `<offset> PRECEDING`.
   *
   * Numeric offsets are query parameters. Pass an {@link Expression}
   * (for example `sql.lit(1)`) to inline a literal.
   */
  preceding(offset: FrameBoundOffset): CompletedOverFrameBuilder {
    return new CompletedOverFrameBuilder({
      start: offsetBound('preceding', offset),
    })
  }

  /**
   * `CURRENT ROW`.
   */
  currentRow(): CompletedOverFrameBuilder {
    return new CompletedOverFrameBuilder({
      start: FrameBoundNode.create('current row'),
    })
  }

  /**
   * `<offset> FOLLOWING`.
   *
   * Numeric offsets are query parameters. Pass an {@link Expression}
   * to inline a literal.
   */
  following(offset: FrameBoundOffset): CompletedOverFrameBuilder {
    return new CompletedOverFrameBuilder({
      start: offsetBound('following', offset),
    })
  }

  /**
   * `UNBOUNDED FOLLOWING`.
   */
  unboundedFollowing(): CompletedOverFrameBuilder {
    return new CompletedOverFrameBuilder({
      start: FrameBoundNode.create('unbounded following'),
    })
  }

  /**
   * Starts `BETWEEN UNBOUNDED PRECEDING AND ...`.
   */
  betweenUnboundedPreceding(): BetweenOverFrameBuilder {
    return new BetweenOverFrameBuilder({
      start: FrameBoundNode.create('unbounded preceding'),
    })
  }

  /**
   * Starts `BETWEEN <offset> PRECEDING AND ...`.
   */
  betweenPreceding(offset: FrameBoundOffset): BetweenOverFrameBuilder {
    return new BetweenOverFrameBuilder({
      start: offsetBound('preceding', offset),
    })
  }

  /**
   * Starts `BETWEEN CURRENT ROW AND ...`.
   */
  betweenCurrentRow(): BetweenOverFrameBuilder {
    return new BetweenOverFrameBuilder({
      start: FrameBoundNode.create('current row'),
    })
  }

  /**
   * Starts `BETWEEN <offset> FOLLOWING AND ...`.
   */
  betweenFollowing(offset: FrameBoundOffset): BetweenOverFrameBuilder {
    return new BetweenOverFrameBuilder({
      start: offsetBound('following', offset),
    })
  }
}

/**
 * The end bound of a `BETWEEN` frame extent.
 */
export class BetweenOverFrameBuilder {
  readonly #start: FrameBoundNode

  constructor(props: { start: FrameBoundNode }) {
    this.#start = props.start
  }

  andUnboundedPreceding(): CompletedOverFrameBuilder {
    return this.#end(FrameBoundNode.create('unbounded preceding'))
  }

  andPreceding(offset: FrameBoundOffset): CompletedOverFrameBuilder {
    return this.#end(offsetBound('preceding', offset))
  }

  andCurrentRow(): CompletedOverFrameBuilder {
    return this.#end(FrameBoundNode.create('current row'))
  }

  andFollowing(offset: FrameBoundOffset): CompletedOverFrameBuilder {
    return this.#end(offsetBound('following', offset))
  }

  andUnboundedFollowing(): CompletedOverFrameBuilder {
    return this.#end(FrameBoundNode.create('unbounded following'))
  }

  #end(end: FrameBoundNode): CompletedOverFrameBuilder {
    return new CompletedOverFrameBuilder({
      start: this.#start,
      end,
    })
  }
}

interface CompletedOverFrameBuilderProps {
  readonly start: FrameBoundNode
  readonly end?: FrameBoundNode
  readonly exclusion?: OverFrameExclusion
}

/**
 * A finished frame extent. Exclusion modifiers can still be added.
 */
export class CompletedOverFrameBuilder {
  readonly #props: CompletedOverFrameBuilderProps

  constructor(props: CompletedOverFrameBuilderProps) {
    this.#props = freeze(props)
  }

  /**
   * `EXCLUDE CURRENT ROW`.
   */
  excludeCurrentRow(): CompletedOverFrameBuilder {
    return this.#exclude('current row')
  }

  /**
   * `EXCLUDE GROUP`.
   */
  excludeGroup(): CompletedOverFrameBuilder {
    return this.#exclude('group')
  }

  /**
   * `EXCLUDE TIES`.
   */
  excludeTies(): CompletedOverFrameBuilder {
    return this.#exclude('ties')
  }

  /**
   * `EXCLUDE NO OTHERS`.
   */
  excludeNoOthers(): CompletedOverFrameBuilder {
    return this.#exclude('no others')
  }

  /**
   * @internal
   */
  toFrame(unit: OverFrameUnit): OverFrameNode {
    return OverFrameNode.create(
      unit,
      this.#props.start,
      this.#props.end,
      this.#props.exclusion,
    )
  }

  #exclude(exclusion: OverFrameExclusion): CompletedOverFrameBuilder {
    return new CompletedOverFrameBuilder({
      ...this.#props,
      exclusion,
    })
  }
}

function offsetBound(
  bound: Extract<FrameBoundKind, 'preceding' | 'following'>,
  offset: FrameBoundOffset,
): FrameBoundNode {
  return FrameBoundNode.create(bound, parseFrameOffset(offset))
}

function parseFrameOffset(offset: FrameBoundOffset) {
  if (isExpression(offset)) {
    return offset.toOperationNode()
  }

  return ValueNode.create(offset)
}

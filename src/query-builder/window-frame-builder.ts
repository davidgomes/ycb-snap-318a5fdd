import type { Expression } from '../expression/expression.js'
import type { OperationNode } from '../operation-node/operation-node.js'
import type {
  WindowFrameBound,
  WindowFrameClause,
  WindowFrameExclusion,
  WindowFrameMode,
} from '../operation-node/over-node.js'
import { ValueNode } from '../operation-node/value-node.js'
import { freeze, isBigInt, isNumber } from '../util/object-utils.js'

/**
 * Numeric offsets are parameterized. An {@link Expression} is compiled inline,
 * which is how callers pass SQL literals such as `` sql`1` ``.
 */
export type WindowFrameOffset = number | bigint | Expression<any>

export function parseWindowFrameOffset(
  offset: WindowFrameOffset,
): OperationNode {
  if (isNumber(offset) || isBigInt(offset)) {
    return ValueNode.create(offset)
  }

  return offset.toOperationNode()
}

/**
 * Builds the extent (frame) inside an `over` clause.
 *
 * Single-bound methods return a completed frame. `between*` methods must be
 * finished with an `and*` method before the frame can be used.
 */
export class WindowFrameBuilder {
  /**
   * `unbounded preceding`
   *
   * Equivalent to `between unbounded preceding and current row`.
   */
  unboundedPreceding(): CompletedWindowFrame {
    return complete({ type: 'unbounded preceding' })
  }

  /**
   * `<offset> preceding`
   *
   * `offset` is sent as a query parameter when it is a number or bigint.
   * Pass an expression to inline a SQL literal.
   */
  preceding(offset: WindowFrameOffset): CompletedWindowFrame {
    return complete({
      type: 'preceding',
      offset: parseWindowFrameOffset(offset),
    })
  }

  /**
   * `current row`
   */
  currentRow(): CompletedWindowFrame {
    return complete({ type: 'current row' })
  }

  /**
   * `<offset> following`
   *
   * `offset` is sent as a query parameter when it is a number or bigint.
   * Pass an expression to inline a SQL literal.
   */
  following(offset: WindowFrameOffset): CompletedWindowFrame {
    return complete({
      type: 'following',
      offset: parseWindowFrameOffset(offset),
    })
  }

  /**
   * `unbounded following`
   *
   * Equivalent to `between current row and unbounded following`.
   */
  unboundedFollowing(): CompletedWindowFrame {
    return complete({ type: 'unbounded following' })
  }

  /**
   * Starts `between unbounded preceding and ...`.
   */
  betweenUnboundedPreceding(): WindowFrameBetweenBuilder {
    return new WindowFrameBetweenBuilder({ type: 'unbounded preceding' })
  }

  /**
   * Starts `between <offset> preceding and ...`.
   */
  betweenPreceding(offset: WindowFrameOffset): WindowFrameBetweenBuilder {
    return new WindowFrameBetweenBuilder({
      type: 'preceding',
      offset: parseWindowFrameOffset(offset),
    })
  }

  /**
   * Starts `between current row and ...`.
   */
  betweenCurrentRow(): WindowFrameBetweenBuilder {
    return new WindowFrameBetweenBuilder({ type: 'current row' })
  }

  /**
   * Starts `between <offset> following and ...`.
   */
  betweenFollowing(offset: WindowFrameOffset): WindowFrameBetweenBuilder {
    return new WindowFrameBetweenBuilder({
      type: 'following',
      offset: parseWindowFrameOffset(offset),
    })
  }
}

/**
 * The second half of a `between ... and ...` frame bound.
 */
export class WindowFrameBetweenBuilder {
  readonly #start: WindowFrameBound

  constructor(start: WindowFrameBound) {
    this.#start = freeze(start)
  }

  /**
   * `and unbounded preceding`
   */
  andUnboundedPreceding(): CompletedWindowFrame {
    return this.#end({ type: 'unbounded preceding' })
  }

  /**
   * `and <offset> preceding`
   */
  andPreceding(offset: WindowFrameOffset): CompletedWindowFrame {
    return this.#end({
      type: 'preceding',
      offset: parseWindowFrameOffset(offset),
    })
  }

  /**
   * `and current row`
   */
  andCurrentRow(): CompletedWindowFrame {
    return this.#end({ type: 'current row' })
  }

  /**
   * `and <offset> following`
   */
  andFollowing(offset: WindowFrameOffset): CompletedWindowFrame {
    return this.#end({
      type: 'following',
      offset: parseWindowFrameOffset(offset),
    })
  }

  /**
   * `and unbounded following`
   */
  andUnboundedFollowing(): CompletedWindowFrame {
    return this.#end({ type: 'unbounded following' })
  }

  #end(end: WindowFrameBound): CompletedWindowFrame {
    return new CompletedWindowFrame({
      start: this.#start,
      end: freeze(end),
    })
  }
}

/**
 * A finished window frame. Exclusion modifiers can still be added.
 */
export class CompletedWindowFrame {
  readonly #start: WindowFrameBound
  readonly #end?: WindowFrameBound
  readonly #exclusion?: WindowFrameExclusion

  constructor(props: {
    start: WindowFrameBound
    end?: WindowFrameBound
    exclusion?: WindowFrameExclusion
  }) {
    this.#start = props.start
    this.#end = props.end
    this.#exclusion = props.exclusion
  }

  /**
   * `exclude current row`
   */
  excludeCurrentRow(): CompletedWindowFrame {
    return this.#exclude('current row')
  }

  /**
   * `exclude group`
   */
  excludeGroup(): CompletedWindowFrame {
    return this.#exclude('group')
  }

  /**
   * `exclude ties`
   */
  excludeTies(): CompletedWindowFrame {
    return this.#exclude('ties')
  }

  /**
   * `exclude no others`
   */
  excludeNoOthers(): CompletedWindowFrame {
    return this.#exclude('no others')
  }

  /**
   * @internal
   */
  toClause(mode: WindowFrameMode): WindowFrameClause {
    return freeze({
      mode,
      start: this.#start,
      end: this.#end,
      exclusion: this.#exclusion,
    })
  }

  #exclude(exclusion: WindowFrameExclusion): CompletedWindowFrame {
    return new CompletedWindowFrame({
      start: this.#start,
      end: this.#end,
      exclusion,
    })
  }
}

function complete(start: WindowFrameBound): CompletedWindowFrame {
  return new CompletedWindowFrame({ start: freeze(start) })
}

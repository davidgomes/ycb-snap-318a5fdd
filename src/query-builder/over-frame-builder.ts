import type { Expression } from '../expression/expression.js'
import { isExpression } from '../expression/expression.js'
import type { OperationNode } from '../operation-node/operation-node.js'
import type {
  OverFrame,
  OverFrameBound,
  OverFrameExclusion,
  OverFrameMode,
} from '../operation-node/over-node.js'
import { ValueNode } from '../operation-node/value-node.js'
import { freeze } from '../util/object-utils.js'

export type FrameOffset = number | bigint | Expression<any>

/**
 * A finished window-frame extent. Single-bound calls and completed
 * `between ... and ...` calls both produce this builder, which can still
 * receive an exclusion modifier.
 */
export class CompletedOverFrame {
  readonly #frame: OverFrame

  constructor(frame: OverFrame) {
    this.#frame = frame
  }

  /**
   * Adds `exclude current row` to the frame.
   */
  excludeCurrentRow(): CompletedOverFrame {
    return this.#withExclusion('current row')
  }

  /**
   * Adds `exclude group` to the frame.
   */
  excludeGroup(): CompletedOverFrame {
    return this.#withExclusion('group')
  }

  /**
   * Adds `exclude ties` to the frame.
   */
  excludeTies(): CompletedOverFrame {
    return this.#withExclusion('ties')
  }

  /**
   * Adds `exclude no others` to the frame.
   */
  excludeNoOthers(): CompletedOverFrame {
    return this.#withExclusion('no others')
  }

  toFrame(): OverFrame {
    return this.#frame
  }

  #withExclusion(exclusion: OverFrameExclusion): CompletedOverFrame {
    return new CompletedOverFrame(
      freeze({
        ...this.#frame,
        exclusion,
      }),
    )
  }
}

/**
 * The end bound of a `between` frame extent.
 */
export class BetweenOverFrame {
  readonly #mode: OverFrameMode
  readonly #start: OverFrameBound

  constructor(mode: OverFrameMode, start: OverFrameBound) {
    this.#mode = mode
    this.#start = start
  }

  andUnboundedPreceding(): CompletedOverFrame {
    return this.#end({ kind: 'unbounded preceding' })
  }

  andPreceding(offset: FrameOffset): CompletedOverFrame {
    return this.#end({ kind: 'preceding', offset: parseFrameOffset(offset) })
  }

  andCurrentRow(): CompletedOverFrame {
    return this.#end({ kind: 'current row' })
  }

  andFollowing(offset: FrameOffset): CompletedOverFrame {
    return this.#end({ kind: 'following', offset: parseFrameOffset(offset) })
  }

  andUnboundedFollowing(): CompletedOverFrame {
    return this.#end({ kind: 'unbounded following' })
  }

  #end(end: OverFrameBound): CompletedOverFrame {
    return new CompletedOverFrame(
      freeze({
        mode: this.#mode,
        start: this.#start,
        end,
      }),
    )
  }
}

/**
 * Builds the extent passed to {@link OverBuilder.rows}, {@link OverBuilder.range}
 * and {@link OverBuilder.groups}.
 */
export class OverFrameBuilder {
  readonly #mode: OverFrameMode

  constructor(mode: OverFrameMode) {
    this.#mode = mode
  }

  unboundedPreceding(): CompletedOverFrame {
    return this.#single({ kind: 'unbounded preceding' })
  }

  preceding(offset: FrameOffset): CompletedOverFrame {
    return this.#single({ kind: 'preceding', offset: parseFrameOffset(offset) })
  }

  currentRow(): CompletedOverFrame {
    return this.#single({ kind: 'current row' })
  }

  following(offset: FrameOffset): CompletedOverFrame {
    return this.#single({
      kind: 'following',
      offset: parseFrameOffset(offset),
    })
  }

  unboundedFollowing(): CompletedOverFrame {
    return this.#single({ kind: 'unbounded following' })
  }

  betweenUnboundedPreceding(): BetweenOverFrame {
    return this.#between({ kind: 'unbounded preceding' })
  }

  betweenPreceding(offset: FrameOffset): BetweenOverFrame {
    return this.#between({
      kind: 'preceding',
      offset: parseFrameOffset(offset),
    })
  }

  betweenCurrentRow(): BetweenOverFrame {
    return this.#between({ kind: 'current row' })
  }

  betweenFollowing(offset: FrameOffset): BetweenOverFrame {
    return this.#between({
      kind: 'following',
      offset: parseFrameOffset(offset),
    })
  }

  #single(start: OverFrameBound): CompletedOverFrame {
    return new CompletedOverFrame(
      freeze({
        mode: this.#mode,
        start,
      }),
    )
  }

  #between(start: OverFrameBound): BetweenOverFrame {
    return new BetweenOverFrame(this.#mode, start)
  }
}

function parseFrameOffset(offset: FrameOffset): OperationNode {
  if (isExpression(offset)) {
    return offset.toOperationNode()
  }

  return ValueNode.create(offset)
}

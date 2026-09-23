import type { Expression } from '../expression/expression.js'
import type { FrameMode } from '../operation-node/frame-node.js'
import type { OperationNodeSource } from '../operation-node/operation-node-source.js'
import { OverNode } from '../operation-node/over-node.js'
import { QueryNode } from '../operation-node/query-node.js'
import {
  type DirectedOrderByStringReference,
  type OrderByExpression,
  type OrderByModifiers,
  parseOrderBy,
} from '../parser/order-by-parser.js'
import {
  parsePartitionBy,
  type PartitionByExpression,
  type PartitionByExpressionOrList,
} from '../parser/partition-by-parser.js'
import { freeze } from '../util/object-utils.js'
import { FrameBuilder, type FrameBuilderCallback } from './frame-builder.js'
import type { OrderByInterface } from './order-by-interface.js'

export class OverBuilder<DB, TB extends keyof DB>
  implements OrderByInterface<DB, TB, {}>, OperationNodeSource
{
  readonly #props: OverBuilderProps

  constructor(props: OverBuilderProps) {
    this.#props = freeze(props)
  }

  /**
   * Adds an `order by` clause or item inside the `over` function.
   *
   * ```ts
   * const result = await db
   *   .selectFrom('person')
   *   .select(
   *     (eb) => eb.fn.avg<number>('age').over(
   *       ob => ob.orderBy('first_name', 'asc').orderBy('last_name', 'asc')
   *     ).as('average_age')
   *   )
   *   .execute()
   * ```
   *
   * The generated SQL (PostgreSQL):
   *
   * ```sql
   * select avg("age") over(order by "first_name" asc, "last_name" asc) as "average_age"
   * from "person"
   * ```
   */
  orderBy<OE extends OrderByExpression<DB, TB, {}>>(
    expr: OE,
    modifiers?: OrderByModifiers,
  ): OverBuilder<DB, TB>

  // TODO: remove in v0.29
  /**
   * @deprecated It does ~2-2.6x more compile-time instantiations compared to multiple chained `orderBy(expr, modifiers?)` calls (in `order by` clauses with reasonable item counts), and has broken autocompletion.
   */
  orderBy<
    OE extends
      | OrderByExpression<DB, TB, {}>
      | DirectedOrderByStringReference<DB, TB, {}>,
  >(exprs: ReadonlyArray<OE>): OverBuilder<DB, TB>

  // TODO: remove in v0.29
  /**
   * @deprecated It does ~2.9x more compile-time instantiations compared to a `orderBy(expr, direction)` call.
   */
  orderBy<OE extends DirectedOrderByStringReference<DB, TB, {}>>(
    expr: OE,
  ): OverBuilder<DB, TB>

  // TODO: remove in v0.29
  /**
   * @deprecated Use `orderBy(expr, (ob) => ...)` instead.
   */
  orderBy<OE extends OrderByExpression<DB, TB, {}>>(
    expr: OE,
    modifiers: Expression<any>,
  ): OverBuilder<DB, TB>

  orderBy(...args: any[]): any {
    return new OverBuilder({
      overNode: OverNode.cloneWithOrderByItems(
        this.#props.overNode,
        parseOrderBy(args),
      ),
    })
  }

  clearOrderBy(): OverBuilder<DB, TB> {
    return new OverBuilder({
      overNode: QueryNode.cloneWithoutOrderBy(this.#props.overNode),
    })
  }

  /**
   * Adds partition by clause item/s inside the over function.
   *
   * ```ts
   * const result = await db
   *   .selectFrom('person')
   *   .select(
   *     (eb) => eb.fn.avg<number>('age').over(
   *       ob => ob.partitionBy(['last_name', 'first_name'])
   *     ).as('average_age')
   *   )
   *   .execute()
   * ```
   *
   * The generated SQL (PostgreSQL):
   *
   * ```sql
   * select avg("age") over(partition by "last_name", "first_name") as "average_age"
   * from "person"
   * ```
   */
  partitionBy(
    partitionBy: ReadonlyArray<PartitionByExpression<DB, TB>>,
  ): OverBuilder<DB, TB>

  partitionBy<PE extends PartitionByExpression<DB, TB>>(
    partitionBy: PE,
  ): OverBuilder<DB, TB>

  partitionBy(partitionBy: PartitionByExpressionOrList<DB, TB>): any {
    return new OverBuilder({
      overNode: OverNode.cloneWithPartitionByItems(
        this.#props.overNode,
        parsePartitionBy(partitionBy),
      ),
    })
  }

  /**
   * Adds a `rows` frame clause inside the over function.
   *
   * Numeric offsets are passed as parameters. Pass an expression, such as
   * `sql.lit(2)`, to inline an offset in the SQL.
   *
   * ### Examples
   *
   * ```ts
   * const result = await db
   *   .selectFrom('person')
   *   .select(
   *     (eb) => eb.fn.avg<number>('age').over(
   *       ob => ob
   *         .orderBy('first_name')
   *         .rows((fb) => fb.betweenPreceding(2).andCurrentRow())
   *     ).as('moving_average_age')
   *   )
   *   .execute()
   * ```
   *
   * The generated SQL (PostgreSQL):
   *
   * ```sql
   * select avg("age") over(order by "first_name" rows between $1 preceding and current row) as "moving_average_age"
   * from "person"
   * ```
   */
  rows(frame: FrameBuilderCallback): OverBuilder<DB, TB> {
    return this.#withFrame('rows', frame)
  }

  /**
   * Adds a `range` frame clause inside the over function.
   *
   * See {@link rows} for details.
   *
   * ### Examples
   *
   * ```ts
   * const result = await db
   *   .selectFrom('person')
   *   .select(
   *     (eb) => eb.fn.sum<number>('age').over(
   *       ob => ob
   *         .orderBy('age')
   *         .range((fb) => fb.betweenCurrentRow().andUnboundedFollowing())
   *     ).as('remaining_age')
   *   )
   *   .execute()
   * ```
   *
   * The generated SQL (PostgreSQL):
   *
   * ```sql
   * select sum("age") over(order by "age" range between current row and unbounded following) as "remaining_age"
   * from "person"
   * ```
   */
  range(frame: FrameBuilderCallback): OverBuilder<DB, TB> {
    return this.#withFrame('range', frame)
  }

  /**
   * Adds a `groups` frame clause inside the over function.
   *
   * See {@link rows} for details.
   *
   * ### Examples
   *
   * ```ts
   * const result = await db
   *   .selectFrom('person')
   *   .select(
   *     (eb) => eb.fn.countAll<number>().over(
   *       ob => ob
   *         .orderBy('age')
   *         .groups((fb) => fb.betweenPreceding(1).andFollowing(1).excludeGroup())
   *     ).as('neighbour_count')
   *   )
   *   .execute()
   * ```
   *
   * The generated SQL (PostgreSQL):
   *
   * ```sql
   * select count(*) over(order by "age" groups between $1 preceding and $2 following exclude group) as "neighbour_count"
   * from "person"
   * ```
   */
  groups(frame: FrameBuilderCallback): OverBuilder<DB, TB> {
    return this.#withFrame('groups', frame)
  }

  #withFrame(
    mode: FrameMode,
    frame: FrameBuilderCallback,
  ): OverBuilder<DB, TB> {
    return new OverBuilder({
      overNode: OverNode.cloneWithFrame(
        this.#props.overNode,
        frame(new FrameBuilder({ mode })).toOperationNode(),
      ),
    })
  }

  /**
   * Simply calls the provided function passing `this` as the only argument. `$call` returns
   * what the provided function returns.
   */
  $call<T>(func: (qb: this) => T): T {
    return func(this)
  }

  toOperationNode(): OverNode {
    return this.#props.overNode
  }
}

export interface OverBuilderProps {
  readonly overNode: OverNode
}

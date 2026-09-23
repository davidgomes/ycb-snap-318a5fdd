import { GroupByItemNode } from '../operation-node/group-by-item-node.js'
import {
  GroupByModifierNode,
  type GroupByModifier,
} from '../operation-node/group-by-modifier-node.js'
import {
  expressionBuilder,
  type ExpressionBuilder,
} from '../expression/expression-builder.js'
import { isFunction, isReadonlyArray } from '../util/object-utils.js'
import {
  parseReferenceExpression,
  parseReferenceExpressionOrList,
  type ReferenceExpression,
} from './reference-parser.js'

export type GroupByExpression<DB, TB extends keyof DB, O> =
  | ReferenceExpression<DB, TB>
  | (keyof O & string)

export type GroupByArg<DB, TB extends keyof DB, O> =
  | GroupByExpression<DB, TB, O>
  | ReadonlyArray<GroupByExpression<DB, TB, O>>
  | ((
      eb: ExpressionBuilder<DB, TB>,
    ) => ReadonlyArray<GroupByExpression<DB, TB, O>>)

export type GroupingSetExpression<DB, TB extends keyof DB, O> =
  | GroupByExpression<DB, TB, O>
  | ReadonlyArray<GroupByExpression<DB, TB, O>>

export function parseGroupBy(
  groupBy: GroupByArg<any, any, any>,
): GroupByItemNode[] {
  groupBy = isFunction(groupBy) ? groupBy(expressionBuilder()) : groupBy
  return parseReferenceExpressionOrList(groupBy).map(GroupByItemNode.create)
}

export function parseGroupByModifier(
  modifier: Exclude<GroupByModifier, 'grouping sets'>,
  columns: ReadonlyArray<GroupByExpression<any, any, any>>,
): GroupByItemNode[] {
  return [
    GroupByItemNode.create(
      GroupByModifierNode.create(modifier, [
        columns.map((column) => parseReferenceExpression(column)),
      ]),
    ),
  ]
}

export function parseGroupByGroupingSets(
  sets: ReadonlyArray<GroupingSetExpression<any, any, any>>,
): GroupByItemNode[] {
  return [
    GroupByItemNode.create(
      GroupByModifierNode.create(
        'grouping sets',
        sets.map((set) =>
          isReadonlyArray(set)
            ? set.map((column) => parseReferenceExpression(column))
            : [parseReferenceExpression(set)],
        ),
      ),
    ),
  ]
}

/**
 * Accepts both `groupByCube('a', 'b')` and `groupByCube(['a', 'b'])`.
 */
export function columnsFromArgs(args: readonly unknown[]): readonly unknown[] {
  if (args.length === 1 && isReadonlyArray(args[0])) {
    return args[0]
  }

  return args
}

/**
 * Accepts both rest sets and one array of sets:
 * `groupByGroupingSets(['a', 'b'], ['a'])` and
 * `groupByGroupingSets([['a', 'b'], ['a']])`.
 *
 * A single array of non-arrays stays one set: `groupByGroupingSets(['a', 'b'])`.
 */
export function groupingSetsFromArgs(
  args: readonly unknown[],
): readonly unknown[] {
  if (
    args.length === 1 &&
    isReadonlyArray(args[0]) &&
    args[0].length > 0 &&
    args[0].every((item) => isReadonlyArray(item))
  ) {
    return args[0]
  }

  return args
}

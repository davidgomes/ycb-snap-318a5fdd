import { GroupByItemNode } from '../operation-node/group-by-item-node.js'
import { FunctionNode } from '../operation-node/function-node.js'
import { ListNode } from '../operation-node/list-node.js'
import type { OperationNode } from '../operation-node/operation-node.js'
import { ParensNode } from '../operation-node/parens-node.js'
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

/**
 * One grouping set: a single expression, or a list of expressions.
 * An empty list is the empty grouping set `()`.
 */
export type GroupingSetExpression<DB, TB extends keyof DB, O> =
  | GroupByExpression<DB, TB, O>
  | ReadonlyArray<GroupByExpression<DB, TB, O>>

export function parseGroupBy(
  groupBy: GroupByArg<any, any, any>,
): GroupByItemNode[] {
  groupBy = isFunction(groupBy) ? groupBy(expressionBuilder()) : groupBy
  return parseReferenceExpressionOrList(groupBy).map(GroupByItemNode.create)
}

export function parseGroupByCube(
  columns: ReadonlyArray<GroupByExpression<any, any, any>>,
): GroupByItemNode[] {
  return [createFlatGroupingElement('cube', columns)]
}

export function parseGroupByRollup(
  columns: ReadonlyArray<GroupByExpression<any, any, any>>,
): GroupByItemNode[] {
  return [createFlatGroupingElement('rollup', columns)]
}

export function parseGroupByGroupingSets(
  sets: ReadonlyArray<GroupingSetExpression<any, any, any>>,
): GroupByItemNode[] {
  return [
    GroupByItemNode.create(
      FunctionNode.create(
        'grouping sets',
        sets.map((set) =>
          ParensNode.create(ListNode.create(parseGroupingSetExpressions(set))),
        ),
      ),
    ),
  ]
}

function createFlatGroupingElement(
  name: 'cube' | 'rollup',
  columns: ReadonlyArray<GroupByExpression<any, any, any>>,
): GroupByItemNode {
  return GroupByItemNode.create(
    FunctionNode.create(name, columns.map(parseReferenceExpression)),
  )
}

function parseGroupingSetExpressions(
  set: GroupingSetExpression<any, any, any>,
): OperationNode[] {
  const expressions = isReadonlyArray(set) ? set : [set]
  return expressions.map(parseReferenceExpression)
}

import { GroupByItemNode } from '../operation-node/group-by-item-node.js'
import {
  expressionBuilder,
  type ExpressionBuilder,
} from '../expression/expression-builder.js'
import { isFunction, isReadonlyArray } from '../util/object-utils.js'
import {
  parseReferenceExpressionOrList,
  type ReferenceExpression,
} from './reference-parser.js'
import { FunctionNode } from '../operation-node/function-node.js'
import { TupleNode } from '../operation-node/tuple-node.js'

export type GroupByExpression<DB, TB extends keyof DB, O> =
  | ReferenceExpression<DB, TB>
  | (keyof O & string)

export type GroupByArg<DB, TB extends keyof DB, O> =
  | GroupByExpression<DB, TB, O>
  | ReadonlyArray<GroupByExpression<DB, TB, O>>
  | ((
      eb: ExpressionBuilder<DB, TB>,
    ) => ReadonlyArray<GroupByExpression<DB, TB, O>>)

export function parseGroupBy(
  groupBy: GroupByArg<any, any, any>,
): GroupByItemNode[] {
  groupBy = isFunction(groupBy) ? groupBy(expressionBuilder()) : groupBy
  return parseReferenceExpressionOrList(groupBy).map(GroupByItemNode.create)
}

export type GroupingSetArg<DB, TB extends keyof DB, O> =
  | GroupByExpression<DB, TB, O>
  | ReadonlyArray<GroupByExpression<DB, TB, O>>

export function parseGroupByCube(
  columns: ReadonlyArray<GroupByExpression<any, any, any>>,
): GroupByItemNode[] {
  return [
    GroupByItemNode.create(
      FunctionNode.create('cube', parseReferenceExpressionOrList(columns)),
    ),
  ]
}

export function parseGroupByRollup(
  columns: ReadonlyArray<GroupByExpression<any, any, any>>,
): GroupByItemNode[] {
  return [
    GroupByItemNode.create(
      FunctionNode.create('rollup', parseReferenceExpressionOrList(columns)),
    ),
  ]
}

export function parseGroupByGroupingSets(
  sets: ReadonlyArray<GroupingSetArg<any, any, any>>,
): GroupByItemNode[] {
  return [
    GroupByItemNode.create(
      FunctionNode.create(
        'grouping sets',
        sets.map((set) =>
          TupleNode.create(
            parseReferenceExpressionOrList(isReadonlyArray(set) ? set : [set]),
          ),
        ),
      ),
    ),
  ]
}

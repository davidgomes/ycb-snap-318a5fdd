import { GroupByItemNode } from '../operation-node/group-by-item-node.js'
import { FunctionNode } from '../operation-node/function-node.js'
import { ListNode } from '../operation-node/list-node.js'
import { ParensNode } from '../operation-node/parens-node.js'
import {
  expressionBuilder,
  type ExpressionBuilder,
} from '../expression/expression-builder.js'
import { isFunction } from '../util/object-utils.js'
import {
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

export function parseGroupBy(
  groupBy: GroupByArg<any, any, any>,
): GroupByItemNode[] {
  groupBy = isFunction(groupBy) ? groupBy(expressionBuilder()) : groupBy
  return parseReferenceExpressionOrList(groupBy).map(GroupByItemNode.create)
}

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
  sets: ReadonlyArray<ReadonlyArray<GroupByExpression<any, any, any>>>,
): GroupByItemNode[] {
  return [
    GroupByItemNode.create(
      FunctionNode.create(
        'grouping sets',
        sets.map((set) =>
          ParensNode.create(
            ListNode.create(parseReferenceExpressionOrList(set)),
          ),
        ),
      ),
    ),
  ]
}

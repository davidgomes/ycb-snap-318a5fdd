import { GroupByItemNode } from '../operation-node/group-by-item-node.js'
import { GroupingSetsNode } from '../operation-node/grouping-sets-node.js'
import { TupleNode } from '../operation-node/tuple-node.js'
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

export type GroupingSetExpression<DB, TB extends keyof DB, O> =
  | GroupByExpression<DB, TB, O>
  | ReadonlyArray<GroupByExpression<DB, TB, O>>

export function parseGroupBy(
  groupBy: GroupByArg<any, any, any>,
): GroupByItemNode[] {
  groupBy = isFunction(groupBy) ? groupBy(expressionBuilder()) : groupBy
  return parseReferenceExpressionOrList(groupBy).map(GroupByItemNode.create)
}

export function parseGroupByCubeOrRollup(
  type: 'cube' | 'rollup',
  columns: ReadonlyArray<GroupByExpression<any, any, any>>,
): GroupByItemNode {
  return GroupByItemNode.create(
    GroupingSetsNode.create(type, parseReferenceExpressionOrList(columns)),
  )
}

export function parseGroupByGroupingSets(
  sets: ReadonlyArray<GroupingSetExpression<any, any, any>>,
): GroupByItemNode {
  return GroupByItemNode.create(
    GroupingSetsNode.create(
      'grouping sets',
      sets.map((set) => TupleNode.create(parseReferenceExpressionOrList(set))),
    ),
  )
}

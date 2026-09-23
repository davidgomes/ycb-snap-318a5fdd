import { GroupByItemNode } from '../operation-node/group-by-item-node.js'
import {
  GroupingElementNode,
  type GroupingElementType,
} from '../operation-node/grouping-element-node.js'
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

export function parseGroupBy(
  groupBy: GroupByArg<any, any, any>,
): GroupByItemNode[] {
  groupBy = isFunction(groupBy) ? groupBy(expressionBuilder()) : groupBy
  return parseReferenceExpressionOrList(groupBy).map(GroupByItemNode.create)
}

export type GroupingSetArg<DB, TB extends keyof DB, O> =
  | GroupByExpression<DB, TB, O>
  | ReadonlyArray<GroupByExpression<DB, TB, O>>

export function parseGroupingElement(
  type: Exclude<GroupingElementType, 'grouping sets'>,
  columns: ReadonlyArray<GroupByExpression<any, any, any>>,
): GroupByItemNode {
  return GroupByItemNode.create(
    GroupingElementNode.create(type, parseReferenceExpressionOrList(columns)),
  )
}

export function parseGroupingSets(
  sets: ReadonlyArray<GroupingSetArg<any, any, any>>,
): GroupByItemNode {
  return GroupByItemNode.create(
    GroupingElementNode.create(
      'grouping sets',
      sets.map((set) => TupleNode.create(parseReferenceExpressionOrList(set))),
    ),
  )
}

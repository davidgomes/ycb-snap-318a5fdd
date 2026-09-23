import { GroupByItemNode } from '../operation-node/group-by-item-node.js'
import { ListNode } from '../operation-node/list-node.js'
import { ParensNode } from '../operation-node/parens-node.js'
import { RawNode } from '../operation-node/raw-node.js'
import {
  expressionBuilder,
  type ExpressionBuilder,
} from '../expression/expression-builder.js'
import { isFunction } from '../util/object-utils.js'
import type { OperationNode } from '../operation-node/operation-node.js'
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

export function parseGroupBy(
  groupBy: GroupByArg<any, any, any>,
): GroupByItemNode[] {
  groupBy = isFunction(groupBy) ? groupBy(expressionBuilder()) : groupBy
  return parseReferenceExpressionOrList(groupBy).map((item) =>
    GroupByItemNode.create(item),
  )
}

export function parseGroupByCubeOrRollup(
  modifier: 'cube' | 'rollup',
  columns: ReadonlyArray<GroupByExpression<any, any, any>>,
): GroupByItemNode[] {
  return [
    GroupByItemNode.create(
      ListNode.create(
        columns.map((column) => parseReferenceExpression(column)),
      ),
      modifier,
    ),
  ]
}

export function parseGroupByGroupingSets(
  sets: ReadonlyArray<ReadonlyArray<GroupByExpression<any, any, any>>>,
): GroupByItemNode[] {
  return [
    GroupByItemNode.create(
      ListNode.create(sets.map((set) => parseGroupingSet(set))),
      'grouping sets',
    ),
  ]
}

function parseGroupingSet(
  set: ReadonlyArray<GroupByExpression<any, any, any>>,
): OperationNode {
  if (set.length === 0) {
    return ParensNode.create(RawNode.createWithSql(''))
  }

  if (set.length === 1) {
    return ParensNode.create(parseReferenceExpression(set[0]!))
  }

  return ParensNode.create(
    ListNode.create(set.map((column) => parseReferenceExpression(column))),
  )
}

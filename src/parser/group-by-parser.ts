import { GroupByItemNode } from '../operation-node/group-by-item-node.js'
import { GroupByCubeNode } from '../operation-node/group-by-cube-node.js'
import { GroupByRollupNode } from '../operation-node/group-by-rollup-node.js'
import { GroupByGroupingSetNode } from '../operation-node/group-by-grouping-set-node.js'
import { GroupByGroupingSetsNode } from '../operation-node/group-by-grouping-sets-node.js'
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
      GroupByCubeNode.create(parseReferenceExpressionOrList(columns)),
    ),
  ]
}

export function parseGroupByRollup(
  columns: ReadonlyArray<GroupByExpression<any, any, any>>,
): GroupByItemNode[] {
  return [
    GroupByItemNode.create(
      GroupByRollupNode.create(parseReferenceExpressionOrList(columns)),
    ),
  ]
}

export function parseGroupByGroupingSets(
  sets: ReadonlyArray<
    ReadonlyArray<GroupByExpression<any, any, any>>
  >,
): GroupByItemNode[] {
  return [
    GroupByItemNode.create(
      GroupByGroupingSetsNode.create(
        sets.map((set) =>
          GroupByGroupingSetNode.create(parseReferenceExpressionOrList(set)),
        ),
      ),
    ),
  ]
}

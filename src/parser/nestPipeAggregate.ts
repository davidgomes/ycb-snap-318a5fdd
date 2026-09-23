import { AstNode, ClauseNode, LimitClauseNode, NodeType, PipeClauseNode } from './ast.js';

/**
 * GROUP BY written inside an AGGREGATE pipe step has no pipe of its own.
 * Fold that following clause into the AGGREGATE node so it indents as a sub-clause.
 */
export function nestPipeAggregateGroupBy(nodes: AstNode[]): AstNode[] {
  const result: AstNode[] = [];
  for (const node of nodes.map(transformNode)) {
    const prev = result[result.length - 1];
    if (isGroupByClause(node) && prev && isAggregatePipe(prev)) {
      const aggregate = prev.clause;
      result[result.length - 1] = {
        ...prev,
        clause: { ...aggregate, children: [...aggregate.children, node] },
      };
    } else {
      result.push(node);
    }
  }
  return result;
}

function transformNode(node: AstNode): AstNode {
  switch (node.type) {
    case NodeType.parenthesis:
      return { ...node, children: nestPipeAggregateGroupBy(node.children) };
    case NodeType.clause:
    case NodeType.set_operation:
      return { ...node, children: nestPipeAggregateGroupBy(node.children) };
    case NodeType.pipe_clause:
      return { ...node, clause: transformClause(node.clause) };
    case NodeType.limit_clause:
      return transformLimit(node);
    case NodeType.between_predicate:
      return {
        ...node,
        expr1: nestPipeAggregateGroupBy(node.expr1),
        expr2: nestPipeAggregateGroupBy(node.expr2),
      };
    case NodeType.case_expression:
      return {
        ...node,
        expr: nestPipeAggregateGroupBy(node.expr),
        clauses: node.clauses.map(clause =>
          clause.type === NodeType.case_when
            ? {
                ...clause,
                condition: nestPipeAggregateGroupBy(clause.condition),
                result: nestPipeAggregateGroupBy(clause.result),
              }
            : { ...clause, result: nestPipeAggregateGroupBy(clause.result) }
        ),
      };
    case NodeType.function_call:
      return { ...node, parenthesis: transformNode(node.parenthesis) as typeof node.parenthesis };
    case NodeType.parameterized_data_type:
      return { ...node, parenthesis: transformNode(node.parenthesis) as typeof node.parenthesis };
    case NodeType.array_subscript:
      return { ...node, parenthesis: transformNode(node.parenthesis) as typeof node.parenthesis };
    case NodeType.property_access:
      return {
        ...node,
        object: transformNode(node.object),
        property: transformNode(node.property) as typeof node.property,
      };
    default:
      return node;
  }
}

function transformClause(clause: ClauseNode | LimitClauseNode): ClauseNode | LimitClauseNode {
  if (clause.type === NodeType.limit_clause) {
    return transformLimit(clause);
  }
  return { ...clause, children: nestPipeAggregateGroupBy(clause.children) };
}

function transformLimit(node: LimitClauseNode): LimitClauseNode {
  const count = nestPipeAggregateGroupBy(node.count);
  if (!node.offset) {
    return count === node.count ? node : { ...node, count };
  }
  return { ...node, count, offset: nestPipeAggregateGroupBy(node.offset) };
}

function isGroupByClause(node: AstNode): node is ClauseNode {
  return node.type === NodeType.clause && node.nameKw.text === 'GROUP BY';
}

function isAggregatePipe(node: AstNode): node is PipeClauseNode & { clause: ClauseNode } {
  return (
    node.type === NodeType.pipe_clause &&
    node.clause.type === NodeType.clause &&
    node.clause.nameKw.text === 'AGGREGATE'
  );
}

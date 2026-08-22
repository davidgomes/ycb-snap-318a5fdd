import type { Schema } from '~/schema/index.js'

import type { ISchemaDTO } from '../types.js'

export interface SchemaDTOContext {
  visiting: Set<Schema>
  refs: Map<Schema, string>
  defs: Record<string, ISchemaDTO>
  nextId: number
}

const contextStack: SchemaDTOContext[] = []

export const getSchemaDTOContext = (): SchemaDTOContext | undefined =>
  contextStack[contextStack.length - 1]

export const runWithSchemaDTOContext = <T>(fn: (context: SchemaDTOContext) => T): T => {
  const context: SchemaDTOContext = {
    visiting: new Set(),
    refs: new Map(),
    defs: {},
    nextId: 0
  }

  contextStack.push(context)
  try {
    return fn(context)
  } finally {
    contextStack.pop()
  }
}

export const assignSchemaRef = (context: SchemaDTOContext, schema: Schema): string => {
  const existing = context.refs.get(schema)
  if (existing !== undefined) {
    return existing
  }

  const id = `schema_${context.nextId++}`
  context.refs.set(schema, id)
  return id
}

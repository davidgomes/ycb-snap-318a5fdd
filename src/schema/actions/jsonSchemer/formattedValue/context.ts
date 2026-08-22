import type { Schema } from '~/schema/index.js'

export interface JSONSchemaContext {
  visiting: Set<Schema>
  refs: Map<Schema, string>
  defs: Record<string, Record<string, unknown>>
  nextId: number
}

const contextStack: JSONSchemaContext[] = []

export const getJSONSchemaContext = (): JSONSchemaContext | undefined =>
  contextStack[contextStack.length - 1]

export const runWithJSONSchemaContext = <T>(fn: (context: JSONSchemaContext) => T): T => {
  const existing = contextStack[contextStack.length - 1]
  if (existing !== undefined) {
    return fn(existing)
  }

  const context: JSONSchemaContext = {
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

export const assignJSONSchemaRef = (context: JSONSchemaContext, schema: Schema): string => {
  const existing = context.refs.get(schema)
  if (existing !== undefined) {
    return existing
  }

  const id = `schema_${context.nextId++}`
  context.refs.set(schema, id)
  return id
}

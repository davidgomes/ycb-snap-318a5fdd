import type { ISchemaDTO } from '~/schema/actions/dto/index.js'
import type { Schema } from '~/schema/index.js'

export interface FromSchemaDTOContext {
  defs: Record<string, ISchemaDTO>
  lazyByRef: Map<string, Schema>
  materialized: Map<string, Schema>
}

const contextStack: FromSchemaDTOContext[] = []

export const getFromSchemaDTOContext = (): FromSchemaDTOContext | undefined =>
  contextStack[contextStack.length - 1]

export const runWithFromSchemaDTOContext = <T>(
  defs: Record<string, ISchemaDTO>,
  fn: () => T
): T => {
  const existing = contextStack[contextStack.length - 1]
  if (existing !== undefined) {
    return fn()
  }

  contextStack.push({
    defs,
    lazyByRef: new Map(),
    materialized: new Map()
  })

  try {
    return fn()
  } finally {
    contextStack.pop()
  }
}

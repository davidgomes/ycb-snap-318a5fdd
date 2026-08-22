import type { Schema } from '~/schema/index.js'

import type { ISchemaDTO, SchemaDefinitionDTO } from '../types.js'
import { getAnySchemaDTO } from './any.js'
import { getAnyOfSchemaDTO } from './anyOf.js'
import { getItemSchemaDTO } from './item.js'
import { getListSchemaDTO } from './list.js'
import { getMapSchemaDTO } from './map.js'
import { getPrimitiveSchemaDTO } from './primitive.js'
import { getRecordSchemaDTO } from './record.js'
import { getSetSchemaDTO } from './set.js'

export interface SchemaDTOContext {
  definitions: Record<string, SchemaDefinitionDTO>
  references: Map<Schema, string>
}

export const createSchemaDTOContext = (): SchemaDTOContext => ({
  definitions: {},
  references: new Map()
})

export const getSchemaDTO = (
  schema: Schema,
  context: SchemaDTOContext = createSchemaDTOContext()
): ISchemaDTO => {
  /**
   * @debt feature "handle defaults, links & validators"
   */
  switch (schema.type) {
    case 'lazy': {
      const resolvedSchema = resolveLazySchema(schema)
      let reference = context.references.get(resolvedSchema)

      if (reference === undefined) {
        reference = `schema_${context.references.size}`
        context.references.set(resolvedSchema, reference)
        context.definitions[reference] = getSchemaDTO(
          resolvedSchema,
          context
        ) as SchemaDefinitionDTO
      }

      return { $ref: reference }
    }
    case 'any':
      return getAnySchemaDTO(schema)
    case 'null':
    case 'boolean':
    case 'number':
    case 'string':
    case 'binary':
      return getPrimitiveSchemaDTO(schema)
    case 'set':
      return getSetSchemaDTO(schema, context)
    case 'list':
      return getListSchemaDTO(schema, context)
    case 'map':
      return getMapSchemaDTO(schema, context)
    case 'record':
      return getRecordSchemaDTO(schema, context)
    case 'anyOf':
      return getAnyOfSchemaDTO(schema, context)
    case 'item':
      return getItemSchemaDTO(schema, context)
  }
}

const resolveLazySchema = (schema: Extract<Schema, { type: 'lazy' }>): Schema => {
  let resolvedSchema: Schema = schema.resolve()

  while (resolvedSchema.type === 'lazy') {
    resolvedSchema = resolvedSchema.resolve()
  }

  return resolvedSchema
}

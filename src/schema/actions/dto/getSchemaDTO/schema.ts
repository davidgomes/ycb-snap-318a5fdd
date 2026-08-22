import type { Schema } from '~/schema/index.js'

import type { ISchemaDTO, SchemaDTOOrRef } from '../types.js'
import { getAnySchemaDTO } from './any.js'
import { getAnyOfSchemaDTO } from './anyOf.js'
import { assignSchemaRef, getSchemaDTOContext } from './context.js'
import { getItemSchemaDTO } from './item.js'
import { getListSchemaDTO } from './list.js'
import { getMapSchemaDTO } from './map.js'
import { getPrimitiveSchemaDTO } from './primitive.js'
import { getRecordSchemaDTO } from './record.js'
import { getSetSchemaDTO } from './set.js'

export const getSchemaDTO = (schema: Schema): SchemaDTOOrRef => {
  /**
   * @debt feature "handle defaults, links & validators"
   */
  const context = getSchemaDTOContext()

  if (schema.type === 'lazy') {
    return getSchemaDTO(schema.resolve())
  }

  if (context !== undefined) {
    const existingRef = context.refs.get(schema)
    if (existingRef !== undefined) {
      return { $ref: existingRef }
    }

    if (context.visiting.has(schema)) {
      return { $ref: assignSchemaRef(context, schema) }
    }

    context.visiting.add(schema)
  }

  let schemaDTO: ISchemaDTO

  switch (schema.type) {
    case 'any':
      schemaDTO = getAnySchemaDTO(schema)
      break
    case 'null':
    case 'boolean':
    case 'number':
    case 'string':
    case 'binary':
      schemaDTO = getPrimitiveSchemaDTO(schema)
      break
    case 'set':
      schemaDTO = getSetSchemaDTO(schema)
      break
    case 'list':
      schemaDTO = getListSchemaDTO(schema)
      break
    case 'map':
      schemaDTO = getMapSchemaDTO(schema)
      break
    case 'record':
      schemaDTO = getRecordSchemaDTO(schema)
      break
    case 'anyOf':
      schemaDTO = getAnyOfSchemaDTO(schema)
      break
    case 'item':
      schemaDTO = getItemSchemaDTO(schema)
      break
  }

  if (context !== undefined) {
    context.visiting.delete(schema)

    if (context.refs.has(schema)) {
      context.defs[context.refs.get(schema) as string] = schemaDTO
    }
  }

  return schemaDTO
}

import { DynamoDBToolboxError } from '~/errors/index.js'
import type { ISchemaDTO, SchemaDefinitionDTO } from '~/schema/actions/dto/index.js'
import type { Schema } from '~/schema/index.js'
import { lazy } from '~/schema/lazy/index.js'

import { fromAnySchemaDTO } from './any.js'
import { fromAnyOfSchemaDTO } from './anyOf.js'
import { fromItemSchemaDTO } from './item.js'
import { fromListSchemaDTO } from './list.js'
import { fromMapSchemaDTO } from './map.js'
import { fromPrimitiveSchemaDTO } from './primitive.js'
import { fromRecordSchemaDTO } from './record.js'
import { fromSetSchemaDTO } from './set.js'

export interface FromSchemaDTOContext {
  definitions: Record<string, SchemaDefinitionDTO>
  references: Map<string, Schema>
}

export const createFromSchemaDTOContext = (
  definitions: Record<string, SchemaDefinitionDTO> = {}
): FromSchemaDTOContext => ({
  definitions,
  references: new Map()
})

export const fromSchemaDTO = (
  schemaDTO: ISchemaDTO,
  context: FromSchemaDTOContext = createFromSchemaDTOContext()
): Schema => {
  if ('$ref' in schemaDTO) {
    const { $ref } = schemaDTO
    const definition = context.definitions[$ref]

    if (definition === undefined) {
      throw new DynamoDBToolboxError('schema.dto.unknownRef', {
        message: `Unknown schema reference '${$ref}'.`,
        payload: { ref: $ref }
      })
    }

    const existingSchema = context.references.get($ref)
    if (existingSchema !== undefined) {
      return existingSchema
    }

    const reference = lazy(() => fromSchemaDTO(definition, context))
    context.references.set($ref, reference)
    return reference
  }

  switch (schemaDTO.type) {
    case 'any':
      return fromAnySchemaDTO(schemaDTO)
    case 'null':
    case 'boolean':
    case 'number':
    case 'string':
    case 'binary':
      return fromPrimitiveSchemaDTO(schemaDTO)
    case 'set':
      return fromSetSchemaDTO(schemaDTO, context)
    case 'list':
      return fromListSchemaDTO(schemaDTO, context)
    case 'map':
      return fromMapSchemaDTO(schemaDTO, context)
    case 'record':
      return fromRecordSchemaDTO(schemaDTO, context)
    case 'anyOf':
      return fromAnyOfSchemaDTO(schemaDTO, context)
    case 'item':
      return fromItemSchemaDTO(schemaDTO, context)
  }
}

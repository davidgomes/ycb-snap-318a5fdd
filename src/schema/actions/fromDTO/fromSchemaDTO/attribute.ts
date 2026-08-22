import { DynamoDBToolboxError } from '~/errors/index.js'
import type { SchemaDTOOrRef } from '~/schema/actions/dto/index.js'
import type { Schema } from '~/schema/index.js'
import { lazy } from '~/schema/lazy/index.js'
import { isObject } from '~/utils/validation/isObject.js'
import { isString } from '~/utils/validation/isString.js'

import { fromAnySchemaDTO } from './any.js'
import { fromAnyOfSchemaDTO } from './anyOf.js'
import { getFromSchemaDTOContext } from './context.js'
import { fromItemSchemaDTO } from './item.js'
import { fromListSchemaDTO } from './list.js'
import { fromMapSchemaDTO } from './map.js'
import { fromPrimitiveSchemaDTO } from './primitive.js'
import { fromRecordSchemaDTO } from './record.js'
import { fromSetSchemaDTO } from './set.js'

const isSchemaRefDTO = (schemaDTO: SchemaDTOOrRef): schemaDTO is { $ref: string } =>
  isObject(schemaDTO) && isString((schemaDTO as { $ref?: unknown }).$ref) && !('type' in schemaDTO)

const fromSchemaRefDTO = ($ref: string): Schema => {
  const context = getFromSchemaDTOContext()

  if (context === undefined || !($ref in context.defs)) {
    throw new DynamoDBToolboxError('schema.fromDTO.unknownRef', {
      message: `Unknown schema $ref '${$ref}'.`,
      payload: { $ref }
    })
  }

  const cached = context.lazyByRef.get($ref)
  if (cached !== undefined) {
    return cached
  }

  const lazySchema = lazy(() => {
    const materialized = context.materialized.get($ref)

    if (materialized === undefined) {
      throw new DynamoDBToolboxError('schema.fromDTO.unknownRef', {
        message: `Unknown schema $ref '${$ref}'.`,
        payload: { $ref }
      })
    }

    return materialized
  })

  context.lazyByRef.set($ref, lazySchema)
  context.materialized.set($ref, fromSchemaDTO(context.defs[$ref] as SchemaDTOOrRef))

  return lazySchema
}

export const fromSchemaDTO = (schemaDTO: SchemaDTOOrRef): Schema => {
  if (isSchemaRefDTO(schemaDTO)) {
    return fromSchemaRefDTO(schemaDTO.$ref)
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
      return fromSetSchemaDTO(schemaDTO)
    case 'list':
      return fromListSchemaDTO(schemaDTO)
    case 'map':
      return fromMapSchemaDTO(schemaDTO)
    case 'record':
      return fromRecordSchemaDTO(schemaDTO)
    case 'anyOf':
      return fromAnyOfSchemaDTO(schemaDTO)
    case 'item':
      return fromItemSchemaDTO(schemaDTO)
  }
}

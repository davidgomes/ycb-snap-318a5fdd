import type { ItemSchema } from '~/schema/item/index.js'

import type { ItemSchemaDTO } from '../types.js'
import { getSchemaDTOContext, runWithSchemaDTOContext } from './context.js'
import { getSchemaDTO } from './schema.js'

const buildItemSchemaDTO = (schema: ItemSchema, attachDefs: boolean): ItemSchemaDTO => {
  const context = getSchemaDTOContext()

  return {
    type: 'item',
    attributes: Object.fromEntries(
      Object.entries(schema.attributes).map(([attributeName, attribute]) => [
        attributeName,
        getSchemaDTO(attribute)
      ])
    ) as ItemSchemaDTO['attributes'],
    ...(attachDefs && context !== undefined && Object.keys(context.defs).length > 0
      ? { $schemaDefs: context.defs }
      : {})
  }
}

export const getItemSchemaDTO = (schema: ItemSchema): ItemSchemaDTO => {
  if (getSchemaDTOContext() !== undefined) {
    return buildItemSchemaDTO(schema, false)
  }

  return runWithSchemaDTOContext(() => buildItemSchemaDTO(schema, true))
}

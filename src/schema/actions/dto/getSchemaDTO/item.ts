import type { ItemSchema } from '~/schema/item/index.js'

import type { ItemSchemaDTO } from '../types.js'
import type { SchemaDTOContext } from './schema.js'
import { getSchemaDTO } from './schema.js'

export const getItemSchemaDTO = (
  schema: ItemSchema,
  context?: SchemaDTOContext,
  includeDefinitions = false
): ItemSchemaDTO => {
  const schemaDTOContext = context ?? {
    definitions: {},
    references: new Map()
  }

  return {
    type: 'item',
    attributes: Object.fromEntries(
      Object.entries(schema.attributes).map(([attributeName, attribute]) => [
        attributeName,
        getSchemaDTO(attribute, schemaDTOContext)
      ])
    ) as ItemSchemaDTO['attributes'],
    ...(includeDefinitions && Object.keys(schemaDTOContext.definitions).length > 0
      ? { $schemaDefs: schemaDTOContext.definitions }
      : {})
  }
}

import type { ItemSchemaDTO } from '~/schema/actions/dto/index.js'
import { item } from '~/schema/item/index.js'
import type { ItemSchema } from '~/schema/item/index.js'

import {
  fromSchemaDTO as _fromSchemaDTO,
  createFromSchemaDTOContext
} from './fromSchemaDTO/index.js'

export const fromSchemaDTO = (schemaDTO: ItemSchemaDTO): ItemSchema => {
  const context = createFromSchemaDTOContext(schemaDTO.$schemaDefs)

  return item(
    Object.fromEntries(
      Object.entries(schemaDTO.attributes).map(([attributeName, attributeDTO]) => [
        attributeName,
        _fromSchemaDTO(attributeDTO, context)
      ])
    )
  )
}

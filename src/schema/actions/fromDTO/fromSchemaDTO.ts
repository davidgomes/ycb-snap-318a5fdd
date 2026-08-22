import type { ItemSchemaDTO } from '~/schema/actions/dto/index.js'
import { item } from '~/schema/item/index.js'
import type { ItemSchema_ } from '~/schema/item/index.js'

import { runWithFromSchemaDTOContext } from './fromSchemaDTO/context.js'
import { fromSchemaDTO as _fromSchemaDTO } from './fromSchemaDTO/index.js'

export const fromSchemaDTO = (schemaDTO: ItemSchemaDTO): ItemSchema_ =>
  runWithFromSchemaDTOContext(schemaDTO.$schemaDefs ?? {}, () =>
    item(
      Object.fromEntries(
        Object.entries(schemaDTO.attributes).map(([attributeName, attributeDTO]) => [
          attributeName,
          _fromSchemaDTO(attributeDTO)
        ])
      )
    )
  )

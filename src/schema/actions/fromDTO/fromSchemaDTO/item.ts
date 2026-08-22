import type { ISchemaDTO } from '~/schema/actions/dto/index.js'
import type { ItemSchema } from '~/schema/item/index.js'
import { item } from '~/schema/item/index.js'

import { fromSchemaDTO } from './attribute.js'
import { runWithFromSchemaDTOContext } from './context.js'

type ItemSchemaDTO = Extract<ISchemaDTO, { type: 'item' }>

/**
 * @debt feature "handle defaults, links & validators"
 */
export const fromItemSchemaDTO = ({
  keyDefault,
  putDefault,
  updateDefault,
  keyLink,
  putLink,
  updateLink,
  attributes,
  $schemaDefs
}: ItemSchemaDTO): ItemSchema => {
  keyDefault
  putDefault
  updateDefault
  keyLink
  putLink
  updateLink

  return runWithFromSchemaDTOContext($schemaDefs ?? {}, () =>
    item(
      Object.fromEntries(
        Object.entries(attributes).map(([attributeName, attribute]) => [
          attributeName,
          fromSchemaDTO(attribute)
        ])
      )
    )
  )
}

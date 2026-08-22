import type { ItemSchema } from '~/schema/index.js'
import { SchemaAction } from '~/schema/index.js'

import { createSchemaDTOContext, getItemSchemaDTO } from './getSchemaDTO/index.js'
import type { ItemSchemaDTO } from './types.js'

export class SchemaDTO<SCHEMA extends ItemSchema = ItemSchema>
  extends SchemaAction<SCHEMA>
  implements ItemSchemaDTO
{
  static override actionName = 'dto' as const

  type: ItemSchemaDTO['type']
  attributes: ItemSchemaDTO['attributes']
  $schemaDefs?: ItemSchemaDTO['$schemaDefs']

  constructor(schema: SCHEMA) {
    super(schema)
    this.type = 'item'
    const schemaDTO = getItemSchemaDTO(this.schema, createSchemaDTOContext(), true)
    this.attributes = schemaDTO.attributes
    this.$schemaDefs = schemaDTO.$schemaDefs
  }

  toJSON(): ItemSchemaDTO {
    return {
      type: this.type,
      attributes: this.attributes,
      ...(this.$schemaDefs !== undefined ? { $schemaDefs: this.$schemaDefs } : {})
    }
  }
}

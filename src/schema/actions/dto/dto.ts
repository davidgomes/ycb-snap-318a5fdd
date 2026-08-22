import type { ItemSchema } from '~/schema/index.js'
import { SchemaAction } from '~/schema/index.js'

import { getItemSchemaDTO } from './getSchemaDTO/item.js'
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

    const dto = getItemSchemaDTO(this.schema)
    this.type = dto.type
    this.attributes = dto.attributes
    this.$schemaDefs = dto.$schemaDefs
  }

  toJSON(): ItemSchemaDTO {
    return {
      type: this.type,
      attributes: this.attributes,
      ...(this.$schemaDefs !== undefined ? { $schemaDefs: this.$schemaDefs } : {})
    }
  }
}

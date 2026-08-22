import type { ISchemaDTO } from '~/schema/actions/dto/index.js'
import type { RecordSchema } from '~/schema/record/index.js'
import { record } from '~/schema/record/index.js'
import type { RecordElementSchema, RecordKeySchema } from '~/schema/record/types.js'

import type { FromSchemaDTOContext } from './attribute.js'
import { fromSchemaDTO } from './attribute.js'

type RecordSchemaDTO = Extract<ISchemaDTO, { type: 'record' }>

/**
 * @debt feature "handle defaults, links & validators"
 */
export const fromRecordSchemaDTO = ({
  keyDefault,
  putDefault,
  updateDefault,
  keyLink,
  putLink,
  updateLink,
  keys,
  elements,
  ...props
}: RecordSchemaDTO, context: FromSchemaDTOContext): RecordSchema => {
  keyDefault
  putDefault
  updateDefault
  keyLink
  putLink
  updateLink

  return record(
    fromSchemaDTO(keys, context) as RecordKeySchema,
    fromSchemaDTO(elements, context) as RecordElementSchema,
    props
  )
}

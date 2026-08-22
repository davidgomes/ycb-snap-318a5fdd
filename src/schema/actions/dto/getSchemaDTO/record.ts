import type { RecordSchema } from '~/schema/record/index.js'

import type { RecordSchemaDTO } from '../types.js'
import type { SchemaDTOContext } from './schema.js'
import { getSchemaDTO } from './schema.js'
import { getDefaultsDTO } from './utils.js'

/**
 * @debt feature "handle defaults, links & validators DTOs"
 */
export const getRecordSchemaDTO = (
  schema: RecordSchema,
  context: SchemaDTOContext
): RecordSchemaDTO => {
  const defaultsDTO = getDefaultsDTO(schema)
  const { required, hidden, key, savedAs } = schema.props

  return {
    type: 'record',
    keys: getSchemaDTO(schema.keys, context) as RecordSchemaDTO['keys'],
    elements: getSchemaDTO(schema.elements, context) as RecordSchemaDTO['elements'],
    ...(required !== undefined && required !== 'atLeastOnce' ? { required } : {}),
    ...(hidden !== undefined && hidden ? { hidden } : {}),
    ...(key !== undefined && key ? { key } : {}),
    ...(savedAs !== undefined ? { savedAs } : {}),
    ...defaultsDTO
  }
}

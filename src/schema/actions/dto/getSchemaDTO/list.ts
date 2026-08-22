import type { ListSchema } from '~/schema/list/index.js'

import type { ListSchemaDTO } from '../types.js'
import type { SchemaDTOContext } from './schema.js'
import { getSchemaDTO } from './schema.js'
import { getDefaultsDTO } from './utils.js'

/**
 * @debt feature "handle defaults, links & validators DTOs"
 */
export const getListSchemaDTO = (
  schema: ListSchema,
  context: SchemaDTOContext
): ListSchemaDTO => {
  const defaultsDTO = getDefaultsDTO(schema)
  const { required, hidden, key, savedAs } = schema.props

  return {
    type: 'list',
    elements: getSchemaDTO(schema.elements, context) as ListSchemaDTO['elements'],
    ...(required !== undefined && required !== 'atLeastOnce' ? { required } : {}),
    ...(hidden !== undefined && hidden ? { hidden } : {}),
    ...(key !== undefined && key ? { key } : {}),
    ...(savedAs !== undefined ? { savedAs } : {}),
    ...defaultsDTO
  }
}

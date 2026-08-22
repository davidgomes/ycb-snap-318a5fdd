import type { MapSchema } from '~/schema/map/index.js'

import type { MapSchemaDTO } from '../types.js'
import type { SchemaDTOContext } from './schema.js'
import { getSchemaDTO } from './schema.js'
import { getDefaultsDTO } from './utils.js'

/**
 * @debt feature "handle defaults, links & validators DTOs"
 */
export const getMapSchemaDTO = (schema: MapSchema, context: SchemaDTOContext): MapSchemaDTO => {
  const defaultsDTO = getDefaultsDTO(schema)
  const { required, hidden, key, savedAs } = schema.props

  return {
    type: 'map',
    attributes: Object.fromEntries(
      Object.entries(schema.attributes).map(([attributeName, attribute]) => [
        attributeName,
        getSchemaDTO(attribute, context)
      ])
    ),
    ...(required !== undefined && required !== 'atLeastOnce' ? { required } : {}),
    ...(hidden !== undefined && hidden ? { hidden } : {}),
    ...(key !== undefined && key ? { key } : {}),
    ...(savedAs !== undefined ? { savedAs } : {}),
    ...defaultsDTO
  }
}

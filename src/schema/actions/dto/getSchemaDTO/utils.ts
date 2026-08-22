import type { Schema } from '~/schema/index.js'
import { isFunction } from '~/utils/validation/isFunction.js'

import type { SchemaDefaultsDTO } from '../types.js'

export const getDefaultsDTO = (
  schema: Schema
): Pick<SchemaDefaultsDTO, 'keyDefault' | 'putDefault' | 'updateDefault'> => {
  const defaultsDTO: Pick<SchemaDefaultsDTO, 'keyDefault' | 'putDefault' | 'updateDefault'> = {}

  for (const mode of ['keyDefault', 'putDefault', 'updateDefault'] as const) {
    const modeDefault = schema.props[mode]

    if (modeDefault === undefined) {
      continue
    }

    defaultsDTO[mode] = isFunction(modeDefault)
      ? { defaulterId: 'custom' }
      : { defaulterId: 'value', value: modeDefault }
  }

  return defaultsDTO
}

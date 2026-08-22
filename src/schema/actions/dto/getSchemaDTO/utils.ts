import type { Schema } from '~/schema/index.js'
import { requiredIfConditionsDTO } from '~/schema/utils/requiredIf.js'
import { isFunction } from '~/utils/validation/isFunction.js'

import type { ISchemaDTO } from '../types.js'

export const getRequiredIfDTO = (schema: Schema): ReturnType<typeof requiredIfConditionsDTO> =>
  requiredIfConditionsDTO(schema.props.requiredIf)

export const getDefaultsDTO = (
  schema: Schema
): Pick<ISchemaDTO, 'keyDefault' | 'putDefault' | 'updateDefault'> => {
  const defaultsDTO: Pick<ISchemaDTO, 'keyDefault' | 'putDefault' | 'updateDefault'> = {}

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

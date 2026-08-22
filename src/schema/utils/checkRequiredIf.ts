import { DynamoDBToolboxError } from '~/errors/index.js'
import type { Schema } from '~/schema/index.js'

export const checkRequiredIf = (
  attributes: Record<string, Schema>,
  path: string | undefined,
  schemaType: 'item' | 'map'
): void => {
  for (const [attributeName, attribute] of Object.entries(attributes)) {
    const { requiredIf, key } = attribute.props

    if (requiredIf === undefined) {
      continue
    }

    const attributePath = [path, attributeName].filter(Boolean).join('.')
    const errorCode =
      schemaType === 'item' ? 'schema.item.invalidRequiredIf' : 'schema.map.invalidRequiredIf'

    if (key) {
      throw new DynamoDBToolboxError(errorCode, {
        message: `Invalid ${schemaType} attributes${
          path !== undefined ? ` at path '${path}'` : ''
        }: Key attribute '${attributeName}' cannot use requiredIf.`,
        path: attributePath === '' ? undefined : attributePath,
        payload: { attributeName, reason: 'key' }
      })
    }

    for (const condition of requiredIf) {
      const { attributeName: controllerName } = condition

      if (controllerName === attributeName) {
        throw new DynamoDBToolboxError(errorCode, {
          message: `Invalid ${schemaType} attributes${
            path !== undefined ? ` at path '${path}'` : ''
          }: Attribute '${attributeName}' cannot require itself.`,
          path: attributePath === '' ? undefined : attributePath,
          payload: { attributeName, controllerName, reason: 'self' }
        })
      }

      if (!(controllerName in attributes)) {
        throw new DynamoDBToolboxError(errorCode, {
          message: `Invalid ${schemaType} attributes${
            path !== undefined ? ` at path '${path}'` : ''
          }: requiredIf on '${attributeName}' references unknown sibling '${controllerName}'.`,
          path: attributePath === '' ? undefined : attributePath,
          payload: { attributeName, controllerName, reason: 'unknown' }
        })
      }
    }
  }
}

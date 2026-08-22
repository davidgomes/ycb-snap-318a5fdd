import { DynamoDBToolboxError } from '~/errors/index.js'

import type { MapAttributes } from '../map/types.js'
import type { Schema } from '../types/schema.js'
import type { RequiredIfCondition } from '../types/schemaProps.js'
import { normalizeRequiredIf } from './requiredIf.js'

export const checkRequiredIfProps = (
  attributes: MapAttributes,
  attributeName: string,
  attribute: Schema,
  path?: string
): void => {
  const { requiredIf, key } = attribute.props
  const conditions = normalizeRequiredIf(requiredIf)

  if (conditions.length === 0) {
    return
  }

  const attributePath = [path, attributeName].filter(Boolean).join('.')

  if (key) {
    throw new DynamoDBToolboxError('schema.invalidRequiredIf', {
      message: `Invalid requiredIf${
        attributePath !== '' ? ` at path '${attributePath}'` : ''
      }: Key attributes cannot declare conditional requirements.`,
      path: attributePath !== '' ? attributePath : undefined,
      payload: { attributeName }
    })
  }

  for (const condition of conditions) {
    validateRequiredIfCondition(attributes, attributeName, condition, attributePath)
  }
}

const validateRequiredIfCondition = (
  attributes: MapAttributes,
  attributeName: string,
  { attribute: controllingAttribute }: RequiredIfCondition,
  path: string
): void => {
  if (controllingAttribute === attributeName) {
    throw new DynamoDBToolboxError('schema.invalidRequiredIf', {
      message: `Invalid requiredIf${
        path !== '' ? ` at path '${path}'` : ''
      }: Attribute cannot be conditionally required based on itself.`,
      path: path !== '' ? path : undefined,
      payload: { attributeName, controllingAttribute }
    })
  }

  const controllingSchema = attributes[controllingAttribute]

  if (controllingSchema === undefined) {
    throw new DynamoDBToolboxError('schema.invalidRequiredIf', {
      message: `Invalid requiredIf${
        path !== '' ? ` at path '${path}'` : ''
      }: Controlling attribute '${controllingAttribute}' is not a sibling attribute.`,
      path: path !== '' ? path : undefined,
      payload: { attributeName, controllingAttribute }
    })
  }

  if (controllingSchema.props.key) {
    throw new DynamoDBToolboxError('schema.invalidRequiredIf', {
      message: `Invalid requiredIf${
        path !== '' ? ` at path '${path}'` : ''
      }: Requirements cannot depend on key attributes.`,
      path: path !== '' ? path : undefined,
      payload: { attributeName, controllingAttribute }
    })
  }
}

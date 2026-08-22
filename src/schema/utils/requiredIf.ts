import { DynamoDBToolboxError } from '~/errors/index.js'
import type { ItemSchema } from '~/schema/item/index.js'
import type { MapSchema } from '~/schema/map/index.js'
import type { Schema } from '~/schema/types/schema.js'
import type { RequiredIf, RequiredIfCondition } from '~/schema/types/schemaProps.js'
import { isObject } from '~/utils/validation/isObject.js'

export const appendRequiredIf = (
  requiredIf: RequiredIf | undefined,
  attributeName: string,
  triggerValues: unknown[]
): RequiredIf => [...(requiredIf ?? []), { attributeName, triggerValues }]

export const isRequiredIfTrigger = (controllerValue: unknown, triggerValues: unknown[]): boolean =>
  triggerValues.some(triggerValue => Object.is(controllerValue, triggerValue))

export const isConditionallyRequired = (
  requiredIf: RequiredIf | undefined,
  parentValue: Record<string, unknown>
): boolean => {
  if (requiredIf === undefined) {
    return false
  }

  return requiredIf.some(({ attributeName, triggerValues }) => {
    if (!(attributeName in parentValue)) {
      return false
    }

    return isRequiredIfTrigger(parentValue[attributeName], triggerValues)
  })
}

export const assertRequiredIfSatisfied = (
  attributes: Record<string, Schema>,
  parentValue: Record<string, unknown>,
  path?: string
): void => {
  for (const [attributeName, attribute] of Object.entries(attributes)) {
    if (parentValue[attributeName] !== undefined) {
      continue
    }

    if (!isConditionallyRequired(attribute.props.requiredIf, parentValue)) {
      continue
    }

    const attributePath = [path, attributeName].filter(Boolean).join('.')

    throw new DynamoDBToolboxError('parsing.attributeRequired', {
      message: `Attribute${attributePath !== '' ? ` '${attributePath}'` : ''} is required.`,
      path: attributePath === '' ? undefined : attributePath
    })
  }
}

export const collectRequiredIfExistencePaths = (
  schema: ItemSchema | MapSchema,
  parentValue: unknown,
  pathPrefix: string[] = []
): string[] => {
  if (!isObject(parentValue)) {
    return []
  }

  const paths: string[] = []

  for (const [attributeName, attribute] of Object.entries(schema.attributes)) {
    const { requiredIf } = attribute.props

    if (requiredIf !== undefined && parentValue[attributeName] === undefined) {
      if (isConditionallyRequired(requiredIf, parentValue)) {
        paths.push([...pathPrefix, attributeName].join('.'))
      }
    }

    const childValue = parentValue[attributeName]

    if (attribute.type === 'map' && isObject(childValue)) {
      paths.push(
        ...collectRequiredIfExistencePaths(attribute, childValue, [...pathPrefix, attributeName])
      )
    }
  }

  return paths
}

export const requiredIfConditionsDTO = (
  requiredIf: RequiredIf | undefined
): { requiredIf?: RequiredIfCondition[] } =>
  requiredIf !== undefined && requiredIf.length > 0 ? { requiredIf } : {}

import { z } from 'zod'

import type { ItemSchema, MapSchema, Schema } from '~/schema/index.js'
import { isRequiredIfTriggered, normalizeRequiredIf } from '~/schema/utils/requiredIf.js'

const getRequiredIfAttributes = (containerSchema: MapSchema | ItemSchema): string[] =>
  Object.entries(containerSchema.attributes)
    .filter(([, attribute]) => normalizeRequiredIf(attribute.props.requiredIf).length > 0)
    .map(([attributeName]) => attributeName)

export const withRequiredIf = (
  containerSchema: MapSchema | ItemSchema,
  zodSchema: z.ZodTypeAny
): z.ZodTypeAny => {
  const requiredIfAttributes = getRequiredIfAttributes(containerSchema)

  if (requiredIfAttributes.length === 0) {
    return zodSchema
  }

  return zodSchema.superRefine((input, context) => {
    if (typeof input !== 'object' || input === null) {
      return
    }

    const parentInput = input as Record<string, unknown>

    for (const attributeName of requiredIfAttributes) {
      const attribute = containerSchema.attributes[attributeName]

      if (attribute === undefined || attribute.props.required === 'always') {
        continue
      }

      if (!isRequiredIfTriggered(attribute.props.requiredIf, parentInput)) {
        continue
      }

      if (parentInput[attributeName] !== undefined) {
        continue
      }

      context.addIssue({
        code: z.ZodIssueCode.custom,
        path: [attributeName],
        message: `Attribute '${attributeName}' is required when conditional triggers match.`
      })
    }
  })
}

export const applyRequiredIfRecursively = (
  schema: Schema,
  zodSchema: z.ZodTypeAny
): z.ZodTypeAny => {
  if (schema.type === 'map' || schema.type === 'item') {
    return withRequiredIf(schema, zodSchema)
  }

  return zodSchema
}

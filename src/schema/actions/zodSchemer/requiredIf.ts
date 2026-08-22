import { z } from 'zod'

import type { ItemSchema, MapSchema } from '~/schema/index.js'
import { isConditionallyRequired } from '~/schema/utils/requiredIf.js'

export const withRequiredIf = (
  schema: ItemSchema | MapSchema,
  zodSchema: z.ZodTypeAny,
  attributeNames?: string[]
): z.ZodTypeAny => {
  const conditionalAttributes = Object.entries(schema.attributes).filter(
    ([attributeName]) => attributeNames === undefined || attributeNames.includes(attributeName)
  )

  if (!conditionalAttributes.some(([, attribute]) => attribute.props.requiredIf !== undefined)) {
    return zodSchema
  }

  return zodSchema.superRefine((value, ctx) => {
    if (value === undefined || value === null || typeof value !== 'object') {
      return
    }

    const parentValue = value as Record<string, unknown>

    for (const [attributeName, attribute] of conditionalAttributes) {
      if (parentValue[attributeName] !== undefined) {
        continue
      }

      if (!isConditionallyRequired(attribute.props.requiredIf, parentValue)) {
        continue
      }

      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: [attributeName],
        message: `Attribute '${attributeName}' is required.`
      })
    }
  })
}

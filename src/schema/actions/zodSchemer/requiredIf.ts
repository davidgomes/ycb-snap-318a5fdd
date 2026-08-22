import { z } from 'zod'

import type { ItemSchema, MapSchema } from '~/schema/index.js'
import { isConditionallyRequired } from '~/schema/utils/requiredIf.js'

export const withRequiredIf = (
  schema: ItemSchema | MapSchema,
  zodSchema: z.ZodTypeAny
): z.ZodTypeAny => {
  const hasRequiredIf = Object.values(schema.attributes).some(
    attribute => attribute.props.requiredIf !== undefined
  )

  if (!hasRequiredIf) {
    return zodSchema
  }

  return zodSchema.superRefine((value, ctx) => {
    if (value === undefined || value === null || typeof value !== 'object') {
      return
    }

    const parentValue = value as Record<string, unknown>

    for (const [attributeName, attribute] of Object.entries(schema.attributes)) {
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

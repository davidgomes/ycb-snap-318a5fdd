import type { ItemSchema, MapSchema, Schema } from '~/schema/index.js'

export const getRequiredIfJSONSchema = (
  attributes: ItemSchema['attributes'] | MapSchema['attributes']
): { allOf?: Record<string, unknown>[] } => {
  const allOf: Record<string, unknown>[] = []

  for (const [attributeName, attribute] of Object.entries(attributes) as [string, Schema][]) {
    if (attribute.props.hidden) {
      continue
    }

    for (const { attributeName: controllerName, triggerValues } of attribute.props.requiredIf ??
      []) {
      allOf.push({
        if: {
          properties: {
            [controllerName]:
              triggerValues.length === 1 ? { const: triggerValues[0] } : { enum: triggerValues }
          },
          required: [controllerName]
        },
        then: { required: [attributeName] }
      })
    }
  }

  return allOf.length > 0 ? { allOf } : {}
}

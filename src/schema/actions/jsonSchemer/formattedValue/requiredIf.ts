import type { MapSchema, ItemSchema, Schema } from '~/schema/index.js'
import { normalizeRequiredIf } from '~/schema/utils/requiredIf.js'

type JSONSchemaCondition = Record<string, unknown>

export const getRequiredIfJSONSchemaConditions = (
  containerSchema: MapSchema | ItemSchema,
  attributeName: string,
  attribute: Schema
): JSONSchemaCondition[] => {
  if (attribute.props.required === 'always') {
    return []
  }

  return normalizeRequiredIf(attribute.props.requiredIf).map(({ attribute: controllingAttribute, values }) => ({
    if: {
      properties: {
        [controllingAttribute]: {
          enum: [...values]
        }
      },
      required: [controllingAttribute]
    },
    then: {
      required: [attributeName]
    }
  }))
}

export const getContainerRequiredIfJSONSchema = (
  containerSchema: MapSchema | ItemSchema
): JSONSchemaCondition[] =>
  Object.entries(containerSchema.attributes).flatMap(([attributeName, attribute]) =>
    getRequiredIfJSONSchemaConditions(containerSchema, attributeName, attribute)
  )

export const getRequiredIfJSONSchemaConditionsRec = (schema: Schema): JSONSchemaCondition[] => {
  switch (schema.type) {
    case 'map':
    case 'item':
      return [
        ...getContainerRequiredIfJSONSchema(schema),
        ...Object.values(schema.attributes).flatMap(attribute =>
          getRequiredIfJSONSchemaConditionsRec(attribute)
        )
      ]
    case 'list':
    case 'set':
      return getRequiredIfJSONSchemaConditionsRec(schema.elements)
    case 'record':
      return getRequiredIfJSONSchemaConditionsRec(schema.elements)
    case 'anyOf':
      return schema.elements.flatMap(element => getRequiredIfJSONSchemaConditionsRec(element))
    default:
      return []
  }
}

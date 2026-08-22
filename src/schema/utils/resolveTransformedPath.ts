import { Path } from '~/schema/actions/utils/path.js'
import type { ArrayPath } from '~/schema/actions/utils/types.js'
import type { ItemSchema, MapSchema, Schema } from '~/schema/index.js'

export const resolveTransformedPath = (rootSchema: Schema, logicalPath: string[]): Path => {
  let currentSchema = rootSchema
  const transformedParts: ArrayPath = []

  for (const part of logicalPath) {
    if (currentSchema.type !== 'map' && currentSchema.type !== 'item') {
      break
    }

    const attribute = currentSchema.attributes[part]

    if (attribute === undefined) {
      break
    }

    transformedParts.push(attribute.props.savedAs ?? part)
    currentSchema = attribute
  }

  return Path.fromArray(transformedParts)
}

export const resolveTransformedPathFromContainer = (
  containerSchema: MapSchema | ItemSchema,
  logicalPath: string[]
): Path => resolveTransformedPath(containerSchema, logicalPath)

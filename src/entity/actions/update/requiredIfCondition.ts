import type { SchemaCondition } from '~/schema/actions/parseCondition/index.js'
import type { ItemSchema } from '~/schema/item/index.js'
import { collectRequiredIfExistencePaths } from '~/schema/utils/requiredIf.js'

export const getRequiredIfUpdateCondition = (
  schema: ItemSchema,
  parsedItem: unknown
): SchemaCondition | undefined => {
  const paths = collectRequiredIfExistencePaths(schema, parsedItem)

  if (paths.length === 0) {
    return undefined
  }

  const existsConditions = paths.map(attr => ({ attr, exists: true as const }))

  return existsConditions.length === 1 ? existsConditions[0] : { and: existsConditions }
}

export const mergeUpdateConditions = (
  requiredIfCondition: SchemaCondition | undefined,
  userCondition: SchemaCondition | undefined
): SchemaCondition | undefined => {
  if (requiredIfCondition === undefined) {
    return userCondition
  }

  if (userCondition === undefined) {
    return requiredIfCondition
  }

  return { and: [requiredIfCondition, userCondition] }
}

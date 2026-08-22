import type { RequiredIfCondition } from '../types/schemaProps.js'

export const normalizeRequiredIf = (
  requiredIf: RequiredIfCondition | RequiredIfCondition[] | undefined
): RequiredIfCondition[] => {
  if (requiredIf === undefined) {
    return []
  }

  return Array.isArray(requiredIf) ? requiredIf : [requiredIf]
}

export const matchesTriggerValue = (received: unknown, trigger: unknown): boolean => {
  if (typeof received === 'bigint' || typeof trigger === 'bigint') {
    return received?.toString() === trigger?.toString()
  }

  return Object.is(received, trigger)
}

export const isRequiredIfTriggered = (
  requiredIf: RequiredIfCondition | RequiredIfCondition[] | undefined,
  parentInput: Record<string, unknown> | undefined
): boolean =>
  normalizeRequiredIf(requiredIf).some(({ attribute, values }) => {
    if (parentInput === undefined || !(attribute in parentInput)) {
      return false
    }

    const controllingValue = parentInput[attribute]

    return values.some(triggerValue => matchesTriggerValue(controllingValue, triggerValue))
  })

export const appendRequiredIfCondition = (
  existing: RequiredIfCondition | RequiredIfCondition[] | undefined,
  attribute: string,
  values: readonly unknown[]
): RequiredIfCondition[] => {
  const normalized = normalizeRequiredIf(existing)
  const sameAttributeCondition = normalized.find(condition => condition.attribute === attribute)

  if (sameAttributeCondition === undefined) {
    return [...normalized, { attribute, values: [...values] }]
  }

  const mergedValues = [...sameAttributeCondition.values]

  for (const value of values) {
    if (!mergedValues.some(existingValue => matchesTriggerValue(existingValue, value))) {
      mergedValues.push(value)
    }
  }

  return normalized.map(condition =>
    condition.attribute === attribute ? { attribute, values: mergedValues } : condition
  )
}

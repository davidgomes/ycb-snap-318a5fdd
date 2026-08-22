import { Path } from '~/schema/actions/utils/path.js'
import type { ItemSchema, MapSchema, Schema } from '~/schema/index.js'
import { matchesTriggerValue, normalizeRequiredIf } from '~/schema/utils/requiredIf.js'
import { resolveTransformedPath } from '~/schema/utils/resolveTransformedPath.js'
import { isObject } from '~/utils/validation/isObject.js'

import { $SET, isSetting } from '../symbols/index.js'
import { isExtension } from '../symbols/isExtension.js'
import type { UpdateExpression } from '../expressUpdate/types.js'

interface RequiredIfConditionState {
  conditionExpressions: string[]
  ExpressionAttributeNames: Record<string, string>
  namesCursor: number
  tokens: Record<string, string>
}

const unwrapUpdateValue = (value: unknown): unknown | undefined => {
  if (value === undefined) {
    return undefined
  }

  if (isSetting(value)) {
    return value[$SET]
  }

  if (isExtension(value)) {
    return undefined
  }

  return value
}

const pathTokens = (path: Path, state: RequiredIfConditionState): string => {
  let tokens = ''

  path.arrayPath.forEach((pathPart, index) => {
    if (typeof pathPart === 'number') {
      tokens += `[${pathPart}]`
      return
    }

    let token = state.tokens[pathPart]

    if (token === undefined) {
      token = `#c_${state.namesCursor}`
      state.tokens[pathPart] = token
      state.ExpressionAttributeNames[token] = pathPart
      state.namesCursor++
    }

    if (index > 0) {
      tokens += '.'
    }

    tokens += token
  })

  return tokens
}

const collectRequiredIfConditionsRec = (
  rootSchema: Schema,
  containerSchema: MapSchema | ItemSchema,
  updateInput: Record<string, unknown>,
  logicalPathPrefix: string[],
  state: RequiredIfConditionState
): void => {
  for (const [attrName, rawAttrValue] of Object.entries(updateInput)) {
    const attributeSchema = containerSchema.attributes[attrName]

    if (attributeSchema === undefined) {
      continue
    }

    const unwrappedValue = unwrapUpdateValue(rawAttrValue)

    if (
      unwrappedValue !== undefined &&
      isObject(unwrappedValue) &&
      (attributeSchema.type === 'map' || attributeSchema.type === 'item')
    ) {
      collectRequiredIfConditionsRec(
        rootSchema,
        attributeSchema,
        unwrappedValue,
        [...logicalPathPrefix, attrName],
        state
      )
    }

    if (unwrappedValue === undefined || isExtension(rawAttrValue)) {
      continue
    }

    for (const [dependentName, dependentSchema] of Object.entries(containerSchema.attributes)) {
      if (dependentName === attrName) {
        continue
      }

      if (dependentSchema.props.required === 'always') {
        continue
      }

      const matchingConditions = normalizeRequiredIf(dependentSchema.props.requiredIf).filter(
        condition => condition.attribute === attrName
      )

      if (matchingConditions.length === 0) {
        continue
      }

      const isTriggered = matchingConditions.some(condition =>
        condition.values.some(triggerValue => matchesTriggerValue(unwrappedValue, triggerValue))
      )

      if (!isTriggered) {
        continue
      }

      if (dependentName in updateInput && updateInput[dependentName] !== undefined) {
        continue
      }

      const transformedPath = resolveTransformedPath(rootSchema, [
        ...logicalPathPrefix,
        dependentName
      ])
      state.conditionExpressions.push(`attribute_exists(${pathTokens(transformedPath, state)})`)
    }
  }
}

export const expressRequiredIfConditions = (
  rootSchema: Schema,
  updateInput: Record<string, unknown>
): Pick<UpdateExpression, 'ExpressionAttributeNames'> & {
  ConditionExpression?: string
} => {
  if (rootSchema.type !== 'item' && rootSchema.type !== 'map') {
    return { ExpressionAttributeNames: {} }
  }

  const state: RequiredIfConditionState = {
    conditionExpressions: [],
    ExpressionAttributeNames: {},
    namesCursor: 1,
    tokens: {}
  }

  collectRequiredIfConditionsRec(rootSchema, rootSchema, updateInput, [], state)

  if (state.conditionExpressions.length === 0) {
    return { ExpressionAttributeNames: {} }
  }

  return {
    ConditionExpression: state.conditionExpressions.join(' AND '),
    ExpressionAttributeNames: state.ExpressionAttributeNames
  }
}

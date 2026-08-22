import type { ErrorBlueprint } from '~/errors/blueprint.js'

type DuplicateSavedAsErrorBlueprint = ErrorBlueprint<{
  code: 'schema.item.duplicateSavedAs'
  hasPath: true
  payload: { savedAs: string }
}>

type InvalidRequiredIfErrorBlueprint = ErrorBlueprint<{
  code: 'schema.item.invalidRequiredIf'
  hasPath: true
  payload: { attributeName: string; controllerName?: string; reason: 'key' | 'self' | 'unknown' }
}>

export type ItemSchemaErrorBlueprints =
  | DuplicateSavedAsErrorBlueprint
  | InvalidRequiredIfErrorBlueprint

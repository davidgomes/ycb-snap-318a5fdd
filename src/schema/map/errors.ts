import type { ErrorBlueprint } from '~/errors/blueprint.js'

type DuplicateSavedAsErrorBlueprint = ErrorBlueprint<{
  code: 'schema.map.duplicateSavedAs'
  hasPath: true
  payload: { savedAs: string }
}>

type InvalidRequiredIfErrorBlueprint = ErrorBlueprint<{
  code: 'schema.map.invalidRequiredIf'
  hasPath: true
  payload: { attributeName: string; controllerName?: string; reason: 'key' | 'self' | 'unknown' }
}>

export type MapSchemaErrorBlueprint =
  | DuplicateSavedAsErrorBlueprint
  | InvalidRequiredIfErrorBlueprint

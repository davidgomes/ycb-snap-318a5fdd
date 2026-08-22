import type { ErrorBlueprint } from '~/errors/blueprint.js'

type InvalidResolutionErrorBlueprint = ErrorBlueprint<{
  code: 'schema.lazy.invalidResolution'
  hasPath: true
  payload: undefined
}>

export type LazySchemaErrorBlueprint = InvalidResolutionErrorBlueprint

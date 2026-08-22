import type { ErrorBlueprint } from '~/errors/blueprint.js'

type InvalidResolutionErrorBlueprint = ErrorBlueprint<{
  code: 'schema.lazy.invalidResolution'
  hasPath: true
  payload: { received: unknown }
}>

export type LazySchemaErrorBlueprint = InvalidResolutionErrorBlueprint

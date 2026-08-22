import type { ErrorBlueprint } from '~/errors/blueprint.js'

type UnknownRefErrorBlueprint = ErrorBlueprint<{
  code: 'schema.fromDTO.unknownRef'
  hasPath: false
  payload: { $ref: string }
}>

export type FromDTOErrorBlueprints = UnknownRefErrorBlueprint

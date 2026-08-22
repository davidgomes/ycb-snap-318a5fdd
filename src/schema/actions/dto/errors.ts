import type { ErrorBlueprint } from '~/errors/blueprint.js'

type UnknownReferenceErrorBlueprint = ErrorBlueprint<{
  code: 'schema.dto.unknownRef'
  hasPath: false
  payload: { ref: string }
}>

export type SchemaDTOErrorBlueprint = UnknownReferenceErrorBlueprint

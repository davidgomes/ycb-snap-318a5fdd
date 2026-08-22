import type { LazySchema } from './schema.js'

export type ResolveLazySchema<SCHEMA extends LazySchema> =
  SCHEMA extends LazySchema<infer RESOLVED_SCHEMA> ? RESOLVED_SCHEMA : never

export type ResolvedLazySchema<SCHEMA extends LazySchema> = ResolveLazySchema<SCHEMA>

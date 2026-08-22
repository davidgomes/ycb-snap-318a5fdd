import type { Schema } from '../types/schema.js'
import type { LazySchema } from './schema.js'

export type ResolveLazySchema<SCHEMA extends LazySchema> =
  SCHEMA extends LazySchema<infer GETTER> ? ReturnType<GETTER> : Schema

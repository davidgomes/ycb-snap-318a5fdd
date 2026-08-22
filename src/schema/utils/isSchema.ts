import { isObject } from '~/utils/validation/isObject.js'

import type { Schema } from '../types/schema.js'

const SCHEMA_TYPES = new Set([
  'any',
  'null',
  'boolean',
  'number',
  'string',
  'binary',
  'set',
  'list',
  'map',
  'record',
  'anyOf',
  'item',
  'lazy'
])

export const isSchema = (value: unknown): value is Schema =>
  isObject(value) &&
  typeof value.type === 'string' &&
  SCHEMA_TYPES.has(value.type) &&
  typeof value.check === 'function' &&
  isObject(value.props)

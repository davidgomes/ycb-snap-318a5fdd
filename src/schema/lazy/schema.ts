import { DynamoDBToolboxError } from '~/errors/index.js'

import type { Schema, SchemaProps } from '../types/index.js'
import { checkSchemaProps } from '../utils/checkSchemaProps.js'

type Resolution =
  | { status: 'pending' }
  | { status: 'resolved'; schema: unknown }
  | { status: 'rejected'; error: unknown }

const checkingSchemas = new WeakSet<LazySchema>()

export class LazySchema<
  RESOLVED_SCHEMA extends Schema = Schema,
  PROPS extends SchemaProps = SchemaProps
> {
  type: 'lazy'
  props: PROPS

  private resolution: Resolution = { status: 'pending' }

  constructor(
    protected readonly thunk: () => RESOLVED_SCHEMA,
    props: PROPS
  ) {
    this.type = 'lazy'
    this.props = props
  }

  get checked(): boolean {
    return Object.isFrozen(this.props)
  }

  resolve(): RESOLVED_SCHEMA {
    switch (this.resolution.status) {
      case 'pending': {
        try {
          const schema = this.thunk()
          this.resolution = { status: 'resolved', schema }
          return schema
        } catch (error) {
          this.resolution = { status: 'rejected', error }
          throw error
        }
      }
      case 'resolved':
        return this.resolution.schema as RESOLVED_SCHEMA
      case 'rejected':
        throw this.resolution.error
    }
  }

  check(path?: string): void {
    if (this.checked || checkingSchemas.has(this)) {
      return
    }

    checkSchemaProps(this.props, path)
    checkingSchemas.add(this)

    try {
      const resolvedSchema = this.resolve()

      if (!isSchema(resolvedSchema)) {
        throw new DynamoDBToolboxError('schema.lazy.invalidResolution', {
          message: `Invalid lazy schema resolution${
            path !== undefined ? ` at path '${path}'` : ''
          }: Expected a schema.`,
          path,
          payload: { received: resolvedSchema }
        })
      }

      resolvedSchema.check(path)
      Object.freeze(this.props)
    } finally {
      checkingSchemas.delete(this)
    }
  }
}

const isSchema = (candidate: unknown): candidate is Schema => {
  if (
    typeof candidate !== 'object' ||
    candidate === null ||
    !('type' in candidate) ||
    !('check' in candidate)
  ) {
    return false
  }

  if (typeof candidate.type !== 'string' || typeof candidate.check !== 'function') {
    return false
  }

  switch (candidate.type) {
    case 'any':
    case 'null':
    case 'boolean':
    case 'number':
    case 'string':
    case 'binary':
    case 'set':
    case 'list':
    case 'map':
    case 'record':
    case 'anyOf':
    case 'item':
    case 'lazy':
      return true
    default:
      return false
  }
}

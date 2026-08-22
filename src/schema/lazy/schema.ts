import { DynamoDBToolboxError } from '~/errors/index.js'

import type { Schema, SchemaProps } from '../types/index.js'
import { checkSchemaProps } from '../utils/checkSchemaProps.js'
import { isSchema } from '../utils/isSchema.js'

export class LazySchema<
  GETTER extends () => any = () => Schema,
  PROPS extends SchemaProps = SchemaProps
> {
  type: 'lazy'
  getter: GETTER
  props: PROPS

  private resolved?: Schema
  private didResolve: boolean

  constructor(getter: GETTER, props: PROPS) {
    this.type = 'lazy'
    this.getter = getter
    this.props = props
    this.didResolve = false
  }

  resolve(): ReturnType<GETTER> {
    if (!this.didResolve) {
      this.resolved = this.getter() as Schema
      this.didResolve = true
    }

    return this.resolved as ReturnType<GETTER>
  }

  get checked(): boolean {
    return Object.isFrozen(this.props)
  }

  check(path?: string): void {
    if (this.checked) {
      return
    }

    checkSchemaProps(this.props, path)

    const resolved = this.resolve()

    if (!isSchema(resolved)) {
      throw new DynamoDBToolboxError('schema.lazy.invalidResolution', {
        message: `Invalid lazy schema resolution${
          path !== undefined ? ` at path '${path}'` : ''
        }: Getter must return a Schema.`,
        path
      })
    }

    // Freeze first so recursive checks through this wrapper do not loop
    Object.freeze(this.props)
    ;(resolved as Schema).check(path)
  }
}

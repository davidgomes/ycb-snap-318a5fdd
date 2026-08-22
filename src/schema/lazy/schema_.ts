/**
 * @debt circular "Remove & prevent imports from entity to schema"
 */
import type { UpdateValueInput } from '~/entity/actions/update/types.js'
import type { Paths, SchemaAction, ValidValue } from '~/schema/index.js'
import type { If, NarrowObject, Overwrite, ValueOrGetter } from '~/types/index.js'
import { ifThenElse } from '~/utils/ifThenElse.js'
import { overwrite } from '~/utils/overwrite.js'

import type {
  Always,
  AtLeastOnce,
  Never,
  Schema,
  SchemaProps,
  SchemaRequiredProp,
  Validator
} from '../types/index.js'
import { LazySchema } from './schema.js'

type LazySchemer = <RESOLVED_SCHEMA extends Schema>(
  thunk: () => RESOLVED_SCHEMA
) => LazySchema_<RESOLVED_SCHEMA, {}>

/**
 * Define a lazily resolved schema.
 *
 * @param thunk Function returning the schema to use
 */
export const lazy: LazySchemer = <RESOLVED_SCHEMA extends Schema>(thunk: () => RESOLVED_SCHEMA) =>
  new LazySchema_(thunk, {})

export class LazySchema_<
  RESOLVED_SCHEMA extends Schema = Schema,
  PROPS extends SchemaProps = SchemaProps
> extends LazySchema<RESOLVED_SCHEMA, PROPS> {
  required<NEXT_IS_REQUIRED extends SchemaRequiredProp = AtLeastOnce>(
    nextRequired: NEXT_IS_REQUIRED = 'atLeastOnce' as NEXT_IS_REQUIRED
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { required: NEXT_IS_REQUIRED }>> {
    return new LazySchema_(this.thunk, overwrite(this.props, { required: nextRequired }))
  }

  optional(): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { required: Never }>> {
    return this.required('never')
  }

  hidden<NEXT_HIDDEN extends boolean = true>(
    nextHidden: NEXT_HIDDEN = true as NEXT_HIDDEN
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { hidden: NEXT_HIDDEN }>> {
    return new LazySchema_(this.thunk, overwrite(this.props, { hidden: nextHidden }))
  }

  key<NEXT_KEY extends boolean = true>(
    nextKey: NEXT_KEY = true as NEXT_KEY
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { key: NEXT_KEY; required: Always }>> {
    return new LazySchema_(this.thunk, overwrite(this.props, { key: nextKey, required: 'always' }))
  }

  savedAs<NEXT_SAVED_AS extends string | undefined>(
    nextSavedAs: NEXT_SAVED_AS
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { savedAs: NEXT_SAVED_AS }>> {
    return new LazySchema_(this.thunk, overwrite(this.props, { savedAs: nextSavedAs }))
  }

  keyDefault(
    nextKeyDefault: ValueOrGetter<ValidValue<this, { mode: 'key' }>>
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { keyDefault: unknown }>> {
    return new LazySchema_(
      this.thunk,
      overwrite(this.props, { keyDefault: nextKeyDefault as unknown })
    )
  }

  putDefault(
    nextPutDefault: ValueOrGetter<ValidValue<this>>
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { putDefault: unknown }>> {
    return new LazySchema_(
      this.thunk,
      overwrite(this.props, { putDefault: nextPutDefault as unknown })
    )
  }

  updateDefault(
    nextUpdateDefault: ValueOrGetter<UpdateValueInput<this, { filled: true }>>
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { updateDefault: unknown }>> {
    return new LazySchema_(
      this.thunk,
      overwrite(this.props, { updateDefault: nextUpdateDefault as unknown })
    )
  }

  default(
    nextDefault: ValueOrGetter<
      If<PROPS['key'], ValidValue<this, { mode: 'key' }>, ValidValue<this>>
    >
  ): If<
    PROPS['key'],
    LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { keyDefault: unknown }>>,
    LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { putDefault: unknown }>>
  > {
    return ifThenElse(
      this.props.key as PROPS['key'],
      new LazySchema_(this.thunk, overwrite(this.props, { keyDefault: nextDefault as unknown })),
      new LazySchema_(this.thunk, overwrite(this.props, { putDefault: nextDefault as unknown }))
    )
  }

  keyLink<SCHEMA extends Schema>(
    nextKeyLink: (
      keyInput: ValidValue<SCHEMA, { mode: 'key'; defined: true }>
    ) => ValidValue<this, { mode: 'key' }>
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { keyLink: unknown }>> {
    return new LazySchema_(this.thunk, overwrite(this.props, { keyLink: nextKeyLink as unknown }))
  }

  putLink<SCHEMA extends Schema>(
    nextPutLink: (putItemInput: ValidValue<SCHEMA, { defined: true }>) => ValidValue<this>
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { putLink: unknown }>> {
    return new LazySchema_(this.thunk, overwrite(this.props, { putLink: nextPutLink as unknown }))
  }

  updateLink<SCHEMA extends Schema>(
    nextUpdateLink: (
      updateItemInput: UpdateValueInput<SCHEMA, { defined: true; extended: false }, Paths<SCHEMA>>
    ) => UpdateValueInput<this, { filled: true }>
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { updateLink: unknown }>> {
    return new LazySchema_(
      this.thunk,
      overwrite(this.props, { updateLink: nextUpdateLink as unknown })
    )
  }

  link<SCHEMA extends Schema>(
    nextLink: (
      keyOrPutItemInput: If<
        PROPS['key'],
        ValidValue<SCHEMA, { mode: 'key'; defined: true }>,
        ValidValue<SCHEMA, { defined: true }>
      >
    ) => If<PROPS['key'], ValidValue<this, { mode: 'key' }>, ValidValue<this>>
  ): If<
    PROPS['key'],
    LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { keyLink: unknown }>>,
    LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { putLink: unknown }>>
  > {
    return ifThenElse(
      this.props.key as PROPS['key'],
      new LazySchema_(this.thunk, overwrite(this.props, { keyLink: nextLink as unknown })),
      new LazySchema_(this.thunk, overwrite(this.props, { putLink: nextLink as unknown }))
    )
  }

  keyValidate(
    nextKeyValidator: Validator<ValidValue<this, { mode: 'key'; defined: true }>, this>
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { keyValidator: Validator }>> {
    return new LazySchema_(
      this.thunk,
      overwrite(this.props, { keyValidator: nextKeyValidator as Validator })
    )
  }

  putValidate(
    nextPutValidator: Validator<ValidValue<this, { defined: true }>, this>
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { putValidator: Validator }>> {
    return new LazySchema_(
      this.thunk,
      overwrite(this.props, { putValidator: nextPutValidator as Validator })
    )
  }

  updateValidate(
    nextUpdateValidator: Validator<UpdateValueInput<this, { filled: true }>, this>
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { updateValidator: Validator }>> {
    return new LazySchema_(
      this.thunk,
      overwrite(this.props, { updateValidator: nextUpdateValidator as Validator })
    )
  }

  validate(
    nextValidator: Validator<
      If<
        PROPS['key'],
        ValidValue<this, { mode: 'key'; defined: true }>,
        ValidValue<this, { defined: true }>
      >,
      this
    >
  ): If<
    PROPS['key'],
    LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { keyValidator: Validator }>>,
    LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, { putValidator: Validator }>>
  > {
    return ifThenElse(
      this.props.key as PROPS['key'],
      new LazySchema_(
        this.thunk,
        overwrite(this.props, { keyValidator: nextValidator as Validator })
      ),
      new LazySchema_(
        this.thunk,
        overwrite(this.props, { putValidator: nextValidator as Validator })
      )
    )
  }

  clone<NEXT_PROPS extends SchemaProps = {}>(
    nextProps: NarrowObject<NEXT_PROPS> = {} as NEXT_PROPS
  ): LazySchema_<RESOLVED_SCHEMA, Overwrite<PROPS, NEXT_PROPS>> {
    return new LazySchema_(this.thunk, overwrite(this.props, nextProps))
  }

  build<ACTION extends SchemaAction<this> = SchemaAction<this>>(
    Action: new (schema: this) => ACTION
  ): ACTION {
    return new Action(this)
  }
}

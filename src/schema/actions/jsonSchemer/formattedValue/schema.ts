import type {
  AnyOfSchema,
  AnySchema,
  ItemSchema,
  LazySchema,
  ListSchema,
  MapSchema,
  PrimitiveSchema,
  RecordSchema,
  Schema,
  SetSchema
} from '~/schema/index.js'

import type { FormattedAnyOfJSONSchema } from './anyOf.js'
import { getFormattedAnyOfJSONSchema } from './anyOf.js'
import type { FormattedItemJSONSchema } from './item.js'
import { getFormattedItemJSONSchema } from './item.js'
import type { FormattedListJSONSchema } from './list.js'
import { getFormattedListJSONSchema } from './list.js'
import type { FormattedMapJSONSchema } from './map.js'
import { getFormattedMapJSONSchema } from './map.js'
import type { FormattedPrimitiveJSONSchema } from './primitive.js'
import { getFormattedPrimitiveJSONSchema } from './primitive.js'
import type { FormattedRecordJSONSchema } from './record.js'
import { getFormattedRecordJSONSchema } from './record.js'
import type { FormattedSetJSONSchema } from './set.js'
import { getFormattedSetJSONSchema } from './set.js'

export type FormattedValueJSONSchema<SCHEMA extends Schema> = Schema extends SCHEMA
  ? Record<string, unknown>
  :
      | (SCHEMA extends AnySchema ? {} : never)
      | (SCHEMA extends LazySchema ? { $ref: string } : never)
      | (SCHEMA extends PrimitiveSchema ? FormattedPrimitiveJSONSchema<SCHEMA> : never)
      | (SCHEMA extends SetSchema ? FormattedSetJSONSchema<SCHEMA> : never)
      | (SCHEMA extends ListSchema ? FormattedListJSONSchema<SCHEMA> : never)
      | (SCHEMA extends MapSchema ? FormattedMapJSONSchema<SCHEMA> : never)
      | (SCHEMA extends RecordSchema ? FormattedRecordJSONSchema<SCHEMA> : never)
      | (SCHEMA extends AnyOfSchema ? FormattedAnyOfJSONSchema<SCHEMA> : never)
      | (SCHEMA extends ItemSchema ? FormattedItemJSONSchema<SCHEMA> : never)

export interface JSONSchemaContext {
  definitions: Record<string, Record<string, unknown>>
  references: Map<Schema, string>
}

const createJSONSchemaContext = (): JSONSchemaContext => ({
  definitions: {},
  references: new Map()
})

export const getFormattedValueJSONSchema = <SCHEMA extends Schema>(
  schema: SCHEMA,
  context?: JSONSchemaContext
): FormattedValueJSONSchema<SCHEMA> => {
  type RESPONSE = FormattedValueJSONSchema<SCHEMA>

  const isRoot = context === undefined
  const schemaContext = context ?? createJSONSchemaContext()
  let formattedSchema: RESPONSE

  switch (schema.type) {
    case 'lazy': {
      const resolvedSchema = resolveLazySchema(schema)
      let reference = schemaContext.references.get(resolvedSchema)

      if (reference === undefined) {
        reference = `schema_${schemaContext.references.size}`
        schemaContext.references.set(resolvedSchema, reference)
        schemaContext.definitions[reference] = getFormattedValueJSONSchema(
          resolvedSchema,
          schemaContext
        ) as Record<string, unknown>
      }

      formattedSchema = { $ref: `#/$defs/${reference}` } as RESPONSE
      break
    }
    case 'any':
      formattedSchema = {} as RESPONSE
      break
    case 'null':
    case 'boolean':
    case 'number':
    case 'string':
    case 'binary':
      formattedSchema = getFormattedPrimitiveJSONSchema(schema) as RESPONSE
      break
    case 'set':
      formattedSchema = getFormattedSetJSONSchema(schema, schemaContext) as RESPONSE
      break
    case 'list':
      formattedSchema = getFormattedListJSONSchema(schema, schemaContext) as RESPONSE
      break
    case 'map':
      formattedSchema = getFormattedMapJSONSchema(schema, schemaContext) as RESPONSE
      break
    case 'record':
      formattedSchema = getFormattedRecordJSONSchema(schema, schemaContext) as RESPONSE
      break
    case 'anyOf':
      formattedSchema = getFormattedAnyOfJSONSchema(schema, schemaContext) as RESPONSE
      break
    case 'item':
      formattedSchema = getFormattedItemJSONSchema(schema, schemaContext) as RESPONSE
      break
  }

  if (isRoot && Object.keys(schemaContext.definitions).length > 0) {
    return { ...formattedSchema, $defs: schemaContext.definitions } as RESPONSE
  }

  return formattedSchema
}

const resolveLazySchema = (schema: Extract<Schema, { type: 'lazy' }>): Schema => {
  let resolvedSchema: Schema = schema.resolve()

  while (resolvedSchema.type === 'lazy') {
    resolvedSchema = resolvedSchema.resolve()
  }

  return resolvedSchema
}

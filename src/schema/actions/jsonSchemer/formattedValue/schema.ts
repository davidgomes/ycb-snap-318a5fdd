import type {
  AnyOfSchema,
  AnySchema,
  ItemSchema,
  ListSchema,
  MapSchema,
  PrimitiveSchema,
  RecordSchema,
  Schema,
  SetSchema
} from '~/schema/index.js'

import type { FormattedAnyOfJSONSchema } from './anyOf.js'
import { getFormattedAnyOfJSONSchema } from './anyOf.js'
import { assignJSONSchemaRef, getJSONSchemaContext, runWithJSONSchemaContext } from './context.js'
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
      | (SCHEMA extends PrimitiveSchema ? FormattedPrimitiveJSONSchema<SCHEMA> : never)
      | (SCHEMA extends SetSchema ? FormattedSetJSONSchema<SCHEMA> : never)
      | (SCHEMA extends ListSchema ? FormattedListJSONSchema<SCHEMA> : never)
      | (SCHEMA extends MapSchema ? FormattedMapJSONSchema<SCHEMA> : never)
      | (SCHEMA extends RecordSchema ? FormattedRecordJSONSchema<SCHEMA> : never)
      | (SCHEMA extends AnyOfSchema ? FormattedAnyOfJSONSchema<SCHEMA> : never)
      | (SCHEMA extends ItemSchema ? FormattedItemJSONSchema<SCHEMA> : never)

const getFormattedValueJSONSchemaInContext = <SCHEMA extends Schema>(
  schema: SCHEMA
): FormattedValueJSONSchema<SCHEMA> => {
  type RESPONSE = FormattedValueJSONSchema<SCHEMA>

  const context = getJSONSchemaContext()

  if (schema.type === 'lazy') {
    return getFormattedValueJSONSchema(schema.resolve()) as RESPONSE
  }

  if (context !== undefined) {
    const existingRef = context.refs.get(schema)
    if (existingRef !== undefined) {
      return { $ref: `#/$defs/${existingRef}` } as unknown as RESPONSE
    }

    if (context.visiting.has(schema)) {
      const id = assignJSONSchemaRef(context, schema)
      return { $ref: `#/$defs/${id}` } as unknown as RESPONSE
    }

    context.visiting.add(schema)
  }

  let jsonSchema: FormattedValueJSONSchema<SCHEMA>

  switch (schema.type) {
    case 'any':
      jsonSchema = {} as RESPONSE
      break
    case 'null':
    case 'boolean':
    case 'number':
    case 'string':
    case 'binary':
      jsonSchema = getFormattedPrimitiveJSONSchema(schema) as RESPONSE
      break
    case 'set':
      jsonSchema = getFormattedSetJSONSchema(schema) as RESPONSE
      break
    case 'list':
      jsonSchema = getFormattedListJSONSchema(schema) as RESPONSE
      break
    case 'map':
      jsonSchema = getFormattedMapJSONSchema(schema) as RESPONSE
      break
    case 'record':
      jsonSchema = getFormattedRecordJSONSchema(schema) as RESPONSE
      break
    case 'anyOf':
      jsonSchema = getFormattedAnyOfJSONSchema(schema) as RESPONSE
      break
    case 'item':
      jsonSchema = getFormattedItemJSONSchema(schema) as RESPONSE
      break
  }

  if (context !== undefined) {
    context.visiting.delete(schema)

    if (context.refs.has(schema)) {
      context.defs[context.refs.get(schema) as string] = jsonSchema as Record<string, unknown>
    }
  }

  return jsonSchema
}

export const getFormattedValueJSONSchema = <SCHEMA extends Schema>(
  schema: SCHEMA
): FormattedValueJSONSchema<SCHEMA> => {
  const existingContext = getJSONSchemaContext()
  if (existingContext !== undefined) {
    return getFormattedValueJSONSchemaInContext(schema)
  }

  return runWithJSONSchemaContext(context => {
    const jsonSchema = getFormattedValueJSONSchemaInContext(schema)

    if (Object.keys(context.defs).length === 0) {
      return jsonSchema
    }

    return { ...jsonSchema, $defs: context.defs } as FormattedValueJSONSchema<SCHEMA>
  })
}

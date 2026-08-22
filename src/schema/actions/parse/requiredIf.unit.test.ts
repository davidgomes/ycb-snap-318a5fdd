import {
  DynamoDBToolboxError,
  Entity,
  JSONSchemer,
  Parser,
  PutItemCommand,
  SchemaDTO,
  Table,
  UpdateItemCommand,
  item,
  map,
  string
} from '~/index.js'
import { itemZodParser } from '~/schema/actions/zodSchemer/parser/item.js'

import { expressRequiredIfConditions } from '~/entity/actions/update/requiredIfConditions/expressRequiredIfConditions.js'

describe('requiredIf', () => {
  const table = new Table({
    name: 'required-if-table',
    partitionKey: { name: 'pk', type: 'string' }
  })

  const polymorphicSchema = item({
    pk: string().key(),
    type: string().optional(),
    payload: string().optional().requiredIf('type', 'event', 'command'),
    metadata: map({
      kind: string().optional(),
      detail: string().optional().requiredIf('kind', 'verbose')
    }).optional()
  })

  const entity = new Entity({
    name: 'PolymorphicEntity',
    table,
    schema: polymorphicSchema
  })

  beforeAll(() => {
    entity.schema.check()
  })

  test('check rejects self-referencing requiredIf', () => {
    const invalidSchema = map({
      field: string().optional().requiredIf('field', 'x')
    })

    expect(() => invalidSchema.check()).toThrow(
      expect.objectContaining({ code: 'schema.invalidRequiredIf' })
    )
  })

  test('check rejects missing controlling sibling', () => {
    const invalidSchema = map({
      dependent: string().optional().requiredIf('missing', 'x')
    })

    expect(() => invalidSchema.check()).toThrow(
      expect.objectContaining({ code: 'schema.invalidRequiredIf' })
    )
  })

  test('check rejects requiredIf on key attributes', () => {
    const invalidSchema = item({
      pk: string().key().requiredIf('sk', 'x'),
      sk: string().key()
    })

    expect(() => invalidSchema.check()).toThrow(
      expect.objectContaining({ code: 'schema.invalidRequiredIf' })
    )
  })

  test('put throws when trigger matches and dependent is absent', () => {
    expect(() =>
      polymorphicSchema.build(Parser).parse(
        {
          pk: '1',
          type: 'event'
        },
        { mode: 'put' }
      )
    ).toThrow(expect.objectContaining({ code: 'parsing.attributeRequired' }))
  })

  test('put succeeds when trigger matches and dependent is present', () => {
    expect(() =>
      polymorphicSchema.build(Parser).parse(
        {
          pk: '1',
          type: 'command',
          payload: 'do-something'
        },
        { mode: 'put' }
      )
    ).not.toThrow()
  })

  test('put succeeds when trigger does not match', () => {
    expect(() =>
      polymorphicSchema.build(Parser).parse(
        {
          pk: '1',
          type: 'snapshot'
        },
        { mode: 'put' }
      )
    ).not.toThrow()
  })

  test('put default satisfies conditional requirement', () => {
    const schemaWithDefault = item({
      pk: string().key(),
      type: string().optional(),
      payload: string().optional().putDefault('default').requiredIf('type', 'event')
    })

    expect(() =>
      schemaWithDefault.build(Parser).parse(
        {
          pk: '1',
          type: 'event'
        },
        { mode: 'put' }
      )
    ).not.toThrow()
  })

  test('static required always takes precedence over absent trigger', () => {
    const schema = item({
      pk: string().key(),
      alwaysRequired: string(),
      type: string().optional()
    })

    expect(() =>
      schema.build(Parser).parse(
        {
          pk: '1'
        },
        { mode: 'put' }
      )
    ).toThrow(expect.objectContaining({ code: 'parsing.attributeRequired' }))
  })

  test('chained requiredIf merges trigger values with OR semantics', () => {
    const schema = map({
      type: string().optional(),
      payload: string().optional().requiredIf('type', 'a').requiredIf('type', 'b')
    })

    expect(() =>
      schema.build(Parser).parse(
        {
          type: 'b'
        },
        { mode: 'put' }
      )
    ).toThrow(expect.objectContaining({ code: 'parsing.attributeRequired' }))
  })

  test('update adds attribute_exists conditions for missing dependents', () => {
    const { ConditionExpression, ExpressionAttributeNames } = expressRequiredIfConditions(
      polymorphicSchema,
      {
        pk: '1',
        type: 'event'
      }
    )

    expect(ConditionExpression).toBe('attribute_exists(#c_1)')
    expect(ExpressionAttributeNames).toStrictEqual({ '#c_1': 'payload' })
  })

  test('update resolves savedAs paths for existence validation', () => {
    const schema = item({
      pk: string().key(),
      status: string().optional().savedAs('st'),
      note: string().optional().savedAs('nt').requiredIf('status', 'published')
    })

    const { ConditionExpression, ExpressionAttributeNames } = expressRequiredIfConditions(schema, {
      pk: '1',
      status: 'published'
    })

    expect(ConditionExpression).toBe('attribute_exists(#c_1)')
    expect(ExpressionAttributeNames).toStrictEqual({ '#c_1': 'nt' })
  })

  test('updateItemParams merges requiredIf condition expressions', () => {
    const { ConditionExpression, ExpressionAttributeNames } = entity
      .build(UpdateItemCommand)
      .item({
        pk: '1',
        type: 'event'
      })
      .params()

    expect(ConditionExpression).toBe('attribute_exists(#c_1)')
    expect(ExpressionAttributeNames).toMatchObject({ '#c_1': 'payload' })
  })

  test('DTO round-trip preserves requiredIf', () => {
    const dto = polymorphicSchema.build(SchemaDTO)
    expect(dto.attributes.payload?.requiredIf).toStrictEqual([
      {
        attribute: 'type',
        values: ['event', 'command']
      }
    ])
  })

  test('JSON Schema export includes conditional presence rules', () => {
    const jsonSchema = polymorphicSchema.build(JSONSchemer).formattedValueSchema() as {
      allOf?: unknown[]
    }

    expect(jsonSchema.allOf).toEqual(
      expect.arrayContaining([
        {
          if: {
            properties: {
              type: {
                enum: ['event', 'command']
              }
            },
            required: ['type']
          },
          then: {
            required: ['payload']
          }
        }
      ])
    )
  })

  test('Zod parser enforces conditional requirements', () => {
    const zodSchema = itemZodParser(polymorphicSchema)

    expect(() => zodSchema.parse({ pk: '1', type: 'event' })).toThrow()
    expect(() =>
      zodSchema.parse({ pk: '1', type: 'event', payload: 'ok' })
    ).not.toThrow()
  })

  test('putItemCommand validates polymorphic items', () => {
    expect(() =>
      entity
        .build(PutItemCommand)
        .item({
          pk: '1',
          type: 'event'
        })
        .params()
    ).toThrow(DynamoDBToolboxError)
  })
})

import { DynamoDBToolboxError } from '~/errors/index.js'
import { SchemaDTO } from '~/schema/actions/dto/index.js'
import { Finder } from '~/schema/actions/finder/index.js'
import { Formatter } from '~/schema/actions/format/index.js'
import { fromSchemaDTO } from '~/schema/actions/fromDTO/index.js'
import { JSONSchemer } from '~/schema/actions/jsonSchemer/index.js'
import { Parser } from '~/schema/actions/parse/index.js'
import { ConditionParser } from '~/schema/actions/parseCondition/index.js'
import { ZodSchemer } from '~/schema/actions/zodSchemer/index.js'
import { anyOf, item, list, map, number, string } from '~/schema/index.js'

import { lazy } from './index.js'

describe('lazy schema', () => {
  test('creates a lazy schema with type lazy', () => {
    const schema = lazy(() => map({ name: string() }))
    expect(schema.type).toBe('lazy')
  })

  test('resolve returns the inner schema and caches the thunk', () => {
    const innerSchema = map({ name: string() })
    const thunk = vi.fn(() => innerSchema)
    const schema = lazy(thunk)

    expect(schema.resolve()).toBe(innerSchema)
    schema.resolve()
    schema.resolve()
    expect(thunk).toHaveBeenCalledTimes(1)
  })

  test('supports builder methods', () => {
    const schema = lazy(() => map({ name: string() }))
      .required('always')
      .hidden()
      .savedAs('_l')
    expect(schema.props.required).toBe('always')
    expect(schema.props.hidden).toBe(true)
    expect(schema.props.savedAs).toBe('_l')

    const cloned = schema.clone({ hidden: false })
    expect(cloned.props.required).toBe('always')
    expect(cloned.props.hidden).toBe(false)

    const keySchema = lazy(() => string()).key()
    expect(keySchema.props.key).toBe(true)
    expect(keySchema.props.required).toBe('always')

    const optionalSchema = lazy(() => map({ name: string() })).optional()
    expect(optionalSchema.props.required).toBe('never')
  })

  test('checks self-referencing schemas without looping', () => {
    const treeSchema: any = map({
      value: string(),
      children: list(lazy(() => treeSchema)).optional()
    })

    expect(() => treeSchema.check()).not.toThrow()
  })

  test('throws on invalid resolution', () => {
    const schema = lazy(() => 'not a schema' as never)

    expect(() => schema.check()).toThrow(DynamoDBToolboxError)
    expect(() => schema.check()).toThrow(
      expect.objectContaining({ code: 'schema.lazy.invalidResolution' })
    )
  })

  test('parses and formats recursive data', () => {
    const treeSchema: any = map({
      value: string(),
      children: list(lazy(() => treeSchema)).optional()
    })
    treeSchema.check()

    const data = {
      value: 'root',
      children: [
        {
          value: 'child1',
          children: [{ value: 'grandchild', children: [] }]
        },
        { value: 'child2' }
      ]
    }

    expect(treeSchema.build(Parser).parse(data, { fill: false })).toStrictEqual(data)
    expect(treeSchema.build(Formatter).format(data)).toStrictEqual(data)
    expect(() =>
      treeSchema
        .build(Parser)
        .parse({ value: 'root', children: [{ value: 123, children: [] }] }, { fill: false })
    ).toThrow(DynamoDBToolboxError)
  })

  test('applies lazy schema props for defaults', () => {
    const schema = map({
      label: string(),
      child: lazy(() => map({ label: string() }))
        .optional()
        .putDefault({ label: 'default' })
    })
    schema.check()

    expect(schema.build(Parser).parse({ label: 'test' })).toStrictEqual({
      label: 'test',
      child: { label: 'default' }
    })
  })

  test('serializes recursive schemas with $ref and $schemaDefs', () => {
    const treeSchema: any = map({
      value: string(),
      children: list(lazy(() => treeSchema)).optional()
    })
    const entitySchema = item({
      pk: string().key(),
      tree: treeSchema
    })
    entitySchema.check()

    const json = JSON.parse(JSON.stringify(entitySchema.build(SchemaDTO)))

    expect(json.type).toBe('item')
    expect(json.attributes.tree.type).toBe('map')
    expect(json.attributes.tree.attributes.children.type).toBe('list')
    expect(json.attributes.tree.attributes.children.elements).toHaveProperty('$ref')
    expect(json.attributes.tree.attributes.children.elements).not.toHaveProperty('type')
    expect(json).toHaveProperty('$schemaDefs')
    expect(Object.keys(json.$schemaDefs).length).toBeGreaterThan(0)

    const reconstructed = fromSchemaDTO(json)
    const parsed = reconstructed
      .build(Parser)
      .parse(
        { pk: 'k', tree: { value: 'v', children: [{ value: 'c', children: [] }] } },
        { fill: false }
      ) as { tree: { value: string; children: { value: string }[] } }
    expect(parsed.tree.value).toBe('v')
    expect(parsed.tree.children[0]?.value).toBe('c')
  })

  test('throws on unknown $ref and cleans up context', () => {
    expect(() =>
      fromSchemaDTO({
        type: 'item',
        attributes: {
          pk: { type: 'string' },
          node: { $ref: 'nonexistent_ref' }
        }
      })
    ).toThrow(DynamoDBToolboxError)

    expect(() =>
      fromSchemaDTO({
        type: 'item',
        attributes: {
          pk: { type: 'string' },
          broken: { $ref: 'missing_ref' }
        },
        $schemaDefs: { some_other_ref: { type: 'string' } }
      })
    ).toThrow()

    const treeSchema: any = map({
      value: string(),
      children: list(lazy(() => treeSchema)).optional()
    })
    const entitySchema = item({
      pk: string().key(),
      tree: treeSchema
    })
    entitySchema.check()

    const reconstructed = fromSchemaDTO(JSON.parse(JSON.stringify(entitySchema.build(SchemaDTO))))
    expect(
      (
        reconstructed
          .build(Parser)
          .parse({ pk: 'k', tree: { value: 'v', children: [] } }, { fill: false }) as {
          tree: { value: string }
        }
      ).tree.value
    ).toBe('v')
  })

  test('exports JSON Schema and Zod schemas for recursive data', () => {
    const treeSchema: any = map({
      value: string(),
      children: list(lazy(() => treeSchema)).optional()
    })
    treeSchema.check()

    const jsonSchema = treeSchema.build(JSONSchemer).formattedValueSchema() as unknown as {
      $defs?: unknown
      type: string
      properties: { children: { type: string; items: { $ref?: string } } }
    }

    expect(jsonSchema).toHaveProperty('$defs')
    expect(jsonSchema.type).toBe('object')
    expect(jsonSchema.properties.children.type).toBe('array')
    expect(jsonSchema.properties.children.items).toHaveProperty('$ref')

    const zodParser = treeSchema.build(ZodSchemer).parser()
    expect(() => zodParser.parse({ value: 'root', children: [{ value: 'child' }] })).not.toThrow()
    expect(() => zodParser.parse({ value: 123 })).toThrow()

    const zodFormatter = treeSchema.build(ZodSchemer).formatter()
    expect(() =>
      zodFormatter.parse({ value: 'root', children: [{ value: 'child', children: [] }] })
    ).not.toThrow()
  })

  test('lazy schemas participate in anyOf parsing', () => {
    const dogSchema = map({
      kind: string().enum('dog').const('dog'),
      bark: string()
    })
    const catSchema = map({
      kind: string().enum('cat').const('cat'),
      purr: number()
    })

    const animalSchema = anyOf(
      lazy(() => dogSchema),
      lazy(() => catSchema)
    )
    animalSchema.check()

    expect(
      animalSchema.build(Parser).parse({ kind: 'dog', bark: 'woof' }, { fill: false })
    ).toStrictEqual({ kind: 'dog', bark: 'woof' })
  })

  test('finds sub-schemas and conditions through lazy references', () => {
    const treeSchema: any = map({
      value: string(),
      child: lazy(() => treeSchema).optional()
    })
    treeSchema.check()

    const finder = treeSchema.build(Finder)
    expect(finder.search('value')[0]?.schema.type).toBe('string')
    expect(finder.search('child.value')[0]?.schema.type).toBe('string')
    expect(finder.search('child.child.child.value')).toHaveLength(1)

    const conditionParser = treeSchema.build(ConditionParser)
    expect(conditionParser.parse({ attr: 'value', eq: 'test' }).ConditionExpression).toContain('=')
    expect(
      conditionParser.parse({ attr: 'child.value', eq: 'nested' }).ConditionExpression
    ).toContain('=')
  })
})

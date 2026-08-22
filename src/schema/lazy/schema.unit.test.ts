import { DynamoDBToolboxError } from '~/errors/index.js'
import { SchemaDTO } from '~/schema/actions/dto/index.js'
import { Finder } from '~/schema/actions/finder/index.js'
import { Formatter } from '~/schema/actions/format/index.js'
import { fromSchemaDTO } from '~/schema/actions/fromDTO/index.js'
import { JSONSchemer } from '~/schema/actions/jsonSchemer/index.js'
import { Parser } from '~/schema/actions/parse/index.js'
import { ZodSchemer } from '~/schema/actions/zodSchemer/index.js'
import { item, lazy, list, map, string } from '~/schema/index.js'

describe('lazy schema', () => {
  test('resolves once and checks recursive schemas', () => {
    const thunk = vi.fn()
    const treeSchema = map({
      value: string(),
      children: list(lazy(() => treeSchema)).optional()
    })
    thunk.mockReturnValue(treeSchema)
    const schema = lazy(thunk)

    expect(schema.resolve()).toBe(treeSchema)
    expect(schema.resolve()).toBe(treeSchema)
    expect(thunk).toHaveBeenCalledTimes(1)
    expect(() => treeSchema.check()).not.toThrow()
  })

  test('parses and formats recursive values', () => {
    const treeSchema = map({
      value: string(),
      children: list(lazy(() => treeSchema)).optional()
    })
    treeSchema.check()
    const value = { value: 'root', children: [{ value: 'leaf', children: [] }] }

    expect(treeSchema.build(Parser).parse(value, { fill: false })).toStrictEqual(value)
    expect(treeSchema.build(Formatter).format(value)).toStrictEqual(value)
  })

  test('uses lazy props for defaults', () => {
    const schema = map({
      child: lazy(() => map({ value: string() }))
        .optional()
        .putDefault({ value: 'default' })
    })

    expect(schema.build(Parser).parse({})).toStrictEqual({ child: { value: 'default' } })
  })

  test('serializes and deserializes recursive DTOs', () => {
    const treeSchema = map({
      value: string(),
      children: list(lazy(() => treeSchema)).optional()
    })
    const itemSchema = item({ tree: treeSchema })
    const dto = itemSchema.build(SchemaDTO)
    const serialized = JSON.parse(JSON.stringify(dto))

    expect(serialized.$schemaDefs).toBeDefined()
    expect(serialized.attributes.tree.attributes.children.elements).toEqual({
      $ref: expect.any(String)
    })

    const reconstructed = fromSchemaDTO(serialized)
    const value = { tree: { value: 'root', children: [{ value: 'leaf' }] } }
    expect(reconstructed.build(Parser).parse(value, { fill: false })).toStrictEqual(value)
  })

  test('exports recursive JSON Schema and Zod schemas', () => {
    const treeSchema = map({
      value: string(),
      children: list(lazy(() => treeSchema)).optional()
    })
    treeSchema.check()

    const jsonSchema = treeSchema.build(JSONSchemer).formattedValueSchema() as {
      $defs: Record<string, unknown>
      properties: { children: { items: { $ref: string } } }
    }
    expect(jsonSchema.$defs).toBeDefined()
    expect(jsonSchema.properties.children.items.$ref).toMatch(/^#\/\$defs\//)

    const zodParser = treeSchema.build(ZodSchemer).parser()
    expect(() => zodParser.parse({ value: 'root', children: [{ value: 'leaf' }] })).not.toThrow()
    expect(() => zodParser.parse({ value: 1 })).toThrow()
  })

  test('finds paths and discriminator schemas through lazy references', () => {
    const treeSchema = map({
      value: string(),
      child: lazy(() => treeSchema).optional()
    })
    treeSchema.check()

    expect(treeSchema.build(Finder).search('child.value')).toHaveLength(1)
  })

  test('rejects invalid resolutions and unknown DTO references', () => {
    const invalid = lazy(() => 'invalid' as never)
    expect(() => invalid.check()).toThrow(
      expect.objectContaining({ code: 'schema.lazy.invalidResolution' })
    )

    expect(() =>
      fromSchemaDTO({
        type: 'item',
        attributes: { child: { $ref: 'missing' } }
      })
    ).toThrow(DynamoDBToolboxError)
  })
})

import { DynamoDBToolboxError } from '~/errors/index.js'
import { item, map, string } from '~/schema/index.js'

import { Parser } from './parser.js'

describe('parse - requiredIf', () => {
  const schema = item({
    kind: string().enum('dog', 'cat', 'wolf').optional(),
    breed: string().optional().requiredIf('kind', 'dog'),
    name: string().optional().requiredIf('kind', 'dog').requiredIf('kind', 'wolf')
  })

  test('throws when a trigger matches and the dependent is absent', () => {
    const invalidCall = () => schema.build(Parser).parse({ kind: 'dog' })

    expect(invalidCall).toThrow(DynamoDBToolboxError)
    expect(invalidCall).toThrow(expect.objectContaining({ code: 'parsing.attributeRequired' }))
  })

  test('accepts a matching trigger when the dependent is present', () => {
    expect(schema.build(Parser).parse({ kind: 'dog', breed: 'husky', name: 'rex' })).toStrictEqual({
      kind: 'dog',
      breed: 'husky',
      name: 'rex'
    })
  })

  test('skips evaluation when the controlling attribute is absent', () => {
    expect(schema.build(Parser).parse({})).toStrictEqual({})
  })

  test('does not require the dependent for a non-matching trigger', () => {
    expect(schema.build(Parser).parse({ kind: 'cat' })).toStrictEqual({ kind: 'cat' })
  })

  test('treats chained requiredIf calls as OR', () => {
    const invalidCall = () => schema.build(Parser).parse({ kind: 'wolf' })

    expect(invalidCall).toThrow(DynamoDBToolboxError)
    expect(invalidCall).toThrow(expect.objectContaining({ code: 'parsing.attributeRequired' }))
  })

  test('lets parsing-applied defaults satisfy the requirement', () => {
    const withDefault = item({
      kind: string().enum('dog', 'cat'),
      breed: string().optional().requiredIf('kind', 'dog').putDefault('mutt')
    })

    expect(withDefault.build(Parser).parse({ kind: 'dog' })).toStrictEqual({
      kind: 'dog',
      breed: 'mutt'
    })
  })

  test('keeps required always even when the trigger does not match', () => {
    const always = item({
      kind: string().enum('dog', 'cat').optional(),
      breed: string().required('always').requiredIf('kind', 'dog')
    })

    const invalidCall = () => always.build(Parser).parse({ kind: 'cat' })

    expect(invalidCall).toThrow(DynamoDBToolboxError)
    expect(invalidCall).toThrow(expect.objectContaining({ code: 'parsing.attributeRequired' }))
  })

  test('validates nested maps', () => {
    const nested = item({
      profile: map({
        kind: string().enum('dog', 'cat'),
        breed: string().optional().requiredIf('kind', 'dog')
      })
    })

    const invalidCall = () => nested.build(Parser).parse({ profile: { kind: 'dog' } })

    expect(invalidCall).toThrow(DynamoDBToolboxError)
    expect(invalidCall).toThrow(expect.objectContaining({ code: 'parsing.attributeRequired' }))
  })
})

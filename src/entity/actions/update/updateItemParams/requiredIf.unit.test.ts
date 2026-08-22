import { Entity, Table, UpdateItemCommand, item, map, string } from '~/index.js'

const TestTable = new Table({
  name: 'test-table',
  partitionKey: { type: 'string', name: 'pk' }
})

const TestEntity = new Entity({
  name: 'RequiredIfEntity',
  timestamps: false,
  schema: item({
    id: string().key().savedAs('pk'),
    kind: string().enum('dog', 'cat').optional().savedAs('_k'),
    breed: string().optional().requiredIf('kind', 'dog').savedAs('_b'),
    profile: map({
      kind: string().enum('dog', 'cat').optional(),
      breed: string().optional().requiredIf('kind', 'dog').savedAs('_pb')
    }).optional()
  }),
  table: TestTable
})

describe('updateItemParams - requiredIf', () => {
  test('adds attribute_exists when a trigger is set and the dependent is missing', () => {
    const { ConditionExpression, ExpressionAttributeNames } = TestEntity.build(UpdateItemCommand)
      .item({ id: '1', kind: 'dog' })
      .params()

    expect(ConditionExpression).toBe('attribute_exists(#c_1)')
    expect(ExpressionAttributeNames).toMatchObject({ '#c_1': '_b' })
  })

  test('does not add an existence condition when the dependent is also set', () => {
    const { ConditionExpression } = TestEntity.build(UpdateItemCommand)
      .item({ id: '1', kind: 'dog', breed: 'husky' })
      .params()

    expect(ConditionExpression).toBeUndefined()
  })

  test('resolves nested savedAs paths', () => {
    const { ConditionExpression, ExpressionAttributeNames } = TestEntity.build(UpdateItemCommand)
      .item({ id: '1', profile: { kind: 'dog' } })
      .params()

    expect(ConditionExpression).toBe('attribute_exists(#c_1.#c_2)')
    expect(ExpressionAttributeNames).toMatchObject({
      '#c_1': 'profile',
      '#c_2': '_pb'
    })
  })

  test('combines with a user condition', () => {
    const { ConditionExpression } = TestEntity.build(UpdateItemCommand)
      .item({ id: '1', kind: 'dog' })
      .options({ condition: { attr: 'id', eq: '1' } })
      .params()

    expect(ConditionExpression).toBe('(attribute_exists(#c_1)) AND (#c_2 = :c_1)')
  })
})

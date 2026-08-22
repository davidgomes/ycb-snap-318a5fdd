import { Entity, Table, UpdateTransaction, item, string } from '~/index.js'

const TestTable = new Table({
  name: 'test-table',
  partitionKey: { type: 'string', name: 'pk' }
})

const TestEntity = new Entity({
  name: 'RequiredIfTransactionEntity',
  timestamps: false,
  schema: item({
    id: string().key().savedAs('pk'),
    kind: string().optional().savedAs('_k'),
    breed: string().optional().requiredIf('kind', 'dog').savedAs('_b')
  }),
  table: TestTable
})

describe('transactUpdate - requiredIf', () => {
  test('adds an existence condition for missing dependents', () => {
    const {
      Update: { ConditionExpression, ExpressionAttributeNames }
    } = TestEntity.build(UpdateTransaction).item({ id: '1', kind: 'dog' }).params()

    expect(ConditionExpression).toBe('attribute_exists(#c_1)')
    expect(ExpressionAttributeNames).toMatchObject({ '#c_1': '_b' })
  })

  test('combines the existence condition with a user condition', () => {
    const {
      Update: { ConditionExpression }
    } = TestEntity.build(UpdateTransaction)
      .item({ id: '1', kind: 'dog' })
      .options({ condition: { attr: 'id', eq: '1' } })
      .params()

    expect(ConditionExpression).toBe('(attribute_exists(#c_1)) AND (#c_2 = :c_1)')
  })
})

import { item, string } from '~/schema/index.js'

import { ZodSchemer } from './zodSchemer.js'

describe('zodSchemer - requiredIf', () => {
  const schema = item({
    kind: string().enum('dog', 'cat').optional(),
    breed: string().optional().requiredIf('kind', 'dog')
  })

  test('parser enforces conditional requirements', () => {
    const parser = schema.build(ZodSchemer).parser()

    expect(parser.parse({ kind: 'dog', breed: 'husky' })).toStrictEqual({
      kind: 'dog',
      breed: 'husky'
    })
    expect(parser.parse({ kind: 'cat' })).toStrictEqual({ kind: 'cat' })
    expect(() => parser.parse({ kind: 'dog' })).toThrow()
  })

  test('formatter enforces conditional requirements', () => {
    const formatter = schema.build(ZodSchemer).formatter()

    expect(formatter.parse({ kind: 'dog', breed: 'husky' })).toStrictEqual({
      kind: 'dog',
      breed: 'husky'
    })
    expect(() => formatter.parse({ kind: 'dog' })).toThrow()
  })
})

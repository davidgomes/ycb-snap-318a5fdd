import { item, string } from '~/schema/index.js'

import { JSONSchemer } from '../jsonSchemer.js'

describe('jsonSchemer - requiredIf', () => {
  test('exports if/then required constraints', () => {
    const schema = item({
      kind: string().enum('dog', 'cat'),
      breed: string().optional().requiredIf('kind', 'dog', 'wolf')
    })

    const jsonSchema = schema.build(JSONSchemer).formattedValueSchema()

    expect(jsonSchema).toMatchObject({
      allOf: [
        {
          if: {
            properties: { kind: { enum: ['dog', 'wolf'] } },
            required: ['kind']
          },
          then: { required: ['breed'] }
        }
      ]
    })
  })
})

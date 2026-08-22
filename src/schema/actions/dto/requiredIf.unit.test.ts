import { anyOf, item, nul, number, string } from '~/schema/index.js'

import { fromSchemaDTO } from '../fromDTO/fromSchemaDTO.js'
import { SchemaDTO } from './dto.js'

describe('dto - requiredIf', () => {
  test('round-trips requiredIf on primitive, anyOf and nested map attributes', () => {
    const schema = item({
      kind: string().enum('dog', 'cat'),
      breed: string().optional().requiredIf('kind', 'dog', 'wolf'),
      age: number().optional().requiredIf('kind', 'dog'),
      extra: anyOf(string(), nul()).optional().requiredIf('kind', 'cat')
    })

    const dto = schema.build(SchemaDTO)
    const restored = fromSchemaDTO(JSON.parse(JSON.stringify(dto)))

    expect(restored.attributes.breed!.props.requiredIf).toStrictEqual([
      { attributeName: 'kind', triggerValues: ['dog', 'wolf'] }
    ])
    expect(restored.attributes.age!.props.requiredIf).toStrictEqual([
      { attributeName: 'kind', triggerValues: ['dog'] }
    ])
    expect(restored.attributes.extra!.props.requiredIf).toStrictEqual([
      { attributeName: 'kind', triggerValues: ['cat'] }
    ])
  })
})

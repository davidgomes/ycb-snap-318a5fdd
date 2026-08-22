import type { Overwrite } from '~/types/index.js'
import { overwrite } from '~/utils/overwrite.js'

import type { RequiredIfCondition, SchemaProps } from '../types/schemaProps.js'
import { appendRequiredIfCondition } from './requiredIf.js'

export type RequiredIfPropsOverwrite<PROPS extends SchemaProps = SchemaProps> = Overwrite<
  PROPS,
  { requiredIf: RequiredIfCondition | RequiredIfCondition[] }
>

export const withRequiredIfProp = <
  PROPS extends SchemaProps,
  ATTR extends string,
  const VALUES extends readonly unknown[]
>(
  props: PROPS,
  attribute: ATTR,
  values: VALUES
): RequiredIfPropsOverwrite<PROPS> =>
  overwrite(props, {
    requiredIf: appendRequiredIfCondition(props.requiredIf, attribute, values)
  }) as RequiredIfPropsOverwrite<PROPS>

import type { Validator } from './validator.js'

/**
 * Tag for optional values
 */
export type Never = 'never'

/**
 * Tag for required at least once values
 */
export type AtLeastOnce = 'atLeastOnce'

/**
 * Tag for always required values
 */
export type Always = 'always'

/**
 * Available values for schema `required` properties
 */
export type SchemaRequiredProp = Never | AtLeastOnce | Always

export interface RequiredIfCondition {
  attributeName: string
  triggerValues: unknown[]
}

/**
 * Conditional requirements evaluated against sibling attributes.
 * Multiple entries are combined with OR semantics.
 */
export type RequiredIf = RequiredIfCondition[]

export interface SchemaProps {
  required?: SchemaRequiredProp
  requiredIf?: RequiredIf
  hidden?: boolean
  key?: boolean
  savedAs?: string
  keyDefault?: unknown
  putDefault?: unknown
  updateDefault?: unknown
  keyLink?: unknown
  putLink?: unknown
  updateLink?: unknown
  keyValidator?: Validator
  putValidator?: Validator
  updateValidator?: Validator
}

import {
	describeBranches,
	type JsonSchemaOrBoolean,
	type Traversal
} from "@ark/schema"
import { printable, throwParseError } from "@ark/util"
import { type, type JsonSchema, type Type } from "arktype"
import { parseArrayJsonSchema } from "./array.ts"
import { parseCommonJsonSchema } from "./common.ts"
import { parseConditionalJsonSchema } from "./conditional.ts"
import {
	parseAnyOfJsonSchema,
	parseCompositionJsonSchema
} from "./composition.ts"
import {
	writeJsonSchemaInsufficientKeysMessage,
	writeJsonSchemaUnsupportedTypeMessage
} from "./errors.ts"
import { parseNumberJsonSchema } from "./number.ts"
import { parseObjectJsonSchema } from "./object.ts"
import { resolveJsonSchemaRef, withJsonSchemaRefContext } from "./ref.ts"
import { JsonSchemaScope } from "./scope.ts"
import { parseStringJsonSchema } from "./string.ts"

const implicitObjectKeywords = [
	"properties",
	"required",
	"patternProperties",
	"additionalProperties",
	"maxProperties",
	"minProperties",
	"propertyNames",
	"dependencies",
	"dependentRequired",
	"dependentSchemas"
] as const

const applyImplicitObjectType = (jsonSchema: JsonSchema): JsonSchema => {
	if ("type" in jsonSchema) return jsonSchema
	if (implicitObjectKeywords.some(key => key in jsonSchema))
		return { ...jsonSchema, type: "object" }
	return jsonSchema
}

const numberConstraintKeywords = [
	"minimum",
	"maximum",
	"exclusiveMinimum",
	"exclusiveMaximum",
	"multipleOf"
] as const

const stringConstraintKeywords = ["minLength", "maxLength", "pattern"] as const

const arrayConstraintKeywords = [
	"items",
	"prefixItems",
	"additionalItems",
	"contains",
	"uniqueItems",
	"minItems",
	"maxItems"
] as const

const applyWhen = (matches: (data: unknown) => boolean, schema: Type): Type => {
	const jsonSchemaTypelessKeywordValidator = (
		data: unknown,
		ctx: Traversal
	) => {
		if (!matches(data)) return true
		if (schema.allows(data)) return true
		return ctx.reject({
			expected: schema.description,
			actual: printable(data)
		})
	}
	return type.unknown.narrow(jsonSchemaTypelessKeywordValidator)
}

const parseTypelessKeywords = (jsonSchema: JsonSchema): Type | undefined => {
	if ("type" in jsonSchema) return undefined

	const validators: Type[] = []
	if (numberConstraintKeywords.some(key => key in jsonSchema)) {
		validators.push(
			applyWhen(
				data => typeof data === "number",
				parseNumberJsonSchema.assert({
					...jsonSchema,
					type: "number"
				}) as Type
			)
		)
	}
	if (stringConstraintKeywords.some(key => key in jsonSchema)) {
		validators.push(
			applyWhen(
				data => typeof data === "string",
				parseStringJsonSchema.assert({
					...jsonSchema,
					type: "string"
				}) as Type
			)
		)
	}
	if (arrayConstraintKeywords.some(key => key in jsonSchema)) {
		validators.push(
			applyWhen(
				data => Array.isArray(data),
				parseArrayJsonSchema.assert({
					...jsonSchema,
					type: "array"
				}) as Type
			)
		)
	}
	if (validators.length === 0) return undefined
	return andValidators(...validators)
}

const andValidators = (
	...validators: Array<type.Any | undefined>
): type.Any | undefined =>
	validators.reduce<type.Any | undefined>((acc, validator) => {
		if (acc === undefined) return validator
		if (validator === undefined) return acc
		return acc.and(validator)
	}, undefined)

const jsonSchemaTypeMatcher = type.match
	.in<Extract<JsonSchema, { type?: unknown }>>()
	.at("type")
	.match({
		"unknown[]": jsonSchema =>
			parseCompositionJsonSchema({
				anyOf: jsonSchema.type.map(t => ({ type: t as never }))
			}),
		"'array'": jsonSchema => parseArrayJsonSchema.assert(jsonSchema),
		"'boolean'|'null'": jsonSchema => type(jsonSchema.type),
		"'integer'|'number'": jsonSchema =>
			parseNumberJsonSchema.assert(jsonSchema),
		"'object'": jsonSchema => parseObjectJsonSchema.assert(jsonSchema),
		"'string'": jsonSchema => parseStringJsonSchema.assert(jsonSchema),
		default: () => undefined
	})

export const innerParseJsonSchema = JsonSchemaScope.Schema.pipe(
	(jsonSchema: JsonSchemaOrBoolean): type.Any => {
		if (typeof jsonSchema === "boolean")
			// no runtime value ever passes validation for JSON schema of 'false'
			return jsonSchema ? JsonSchemaScope.Json : type.never

		if (Array.isArray(jsonSchema)) return parseAnyOfJsonSchema(jsonSchema)

		if ("$ref" in jsonSchema) return resolveJsonSchemaRef(jsonSchema.$ref)

		const schema = applyImplicitObjectType(jsonSchema as JsonSchema)

		const preTypeValidator = andValidators(
			parseCommonJsonSchema(schema),
			parseCompositionJsonSchema(schema),
			parseConditionalJsonSchema(schema),
			parseTypelessKeywords(schema)
		)

		if ("type" in schema) {
			const typeValidator = jsonSchemaTypeMatcher(schema as never) as
				| type.Any
				| undefined

			if (typeValidator === undefined) {
				throwParseError(
					writeJsonSchemaUnsupportedTypeMessage(printable(schema.type))
				)
			}

			if (preTypeValidator === undefined) return typeValidator
			return typeValidator.and(preTypeValidator)
		}
		if (preTypeValidator === undefined) {
			const atLeastOneOf = [
				"'type'",
				"'enum'",
				"'const'",
				"'allOf'",
				"'anyOf'",
				"'oneOf'",
				"'not'",
				"'$ref'",
				"'if'"
			]
			throwParseError(
				writeJsonSchemaInsufficientKeysMessage(
					describeBranches(atLeastOneOf, { finalDelimiter: " and " }),
					printable(schema)
				)
			)
		}
		return preTypeValidator
	}
)

export const parseJsonSchema = (
	jsonSchema: JsonSchemaOrBoolean
): type<unknown> =>
	withJsonSchemaRefContext(
		jsonSchema,
		schema => innerParseJsonSchema.assert(schema) as never,
		() => innerParseJsonSchema.assert(jsonSchema) as never
	)

export const jsonSchemaToType = (
	jsonSchema: JsonSchemaOrBoolean
): type<unknown> => parseJsonSchema(jsonSchema)

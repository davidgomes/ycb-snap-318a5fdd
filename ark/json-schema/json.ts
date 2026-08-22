import { describeBranches, type JsonSchemaOrBoolean } from "@ark/schema"
import { printable, throwParseError } from "@ark/util"
import { type, type JsonSchema } from "arktype"
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

const applyImplicitObjectType = (
	jsonSchema: JsonSchema
): JsonSchema => {
	if ("type" in jsonSchema) return jsonSchema
	if (implicitObjectKeywords.some(key => key in jsonSchema))
		return { ...jsonSchema, type: "object" }
	return jsonSchema
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
			parseConditionalJsonSchema(schema)
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

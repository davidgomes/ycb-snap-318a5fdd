import type { JsonSchemaOrBoolean, Traversal } from "@ark/schema"
import { printable } from "@ark/util"
import { type, type JsonSchema, type Type } from "arktype"
import { jsonSchemaToType } from "./json.ts"

const applyBranch = (
	schema: Type,
	data: unknown,
	ctx: Traversal
): boolean => {
	if (schema.allows(data)) return true
	return ctx.reject({
		expected: schema.description,
		actual: printable(data)
	})
}

export const parseConditionalJsonSchema = (
	jsonSchema: JsonSchema
): Type | undefined => {
	if (!("if" in jsonSchema)) return undefined

	const ifSchema = jsonSchemaToType(jsonSchema.if as JsonSchemaOrBoolean)

	if (!("then" in jsonSchema) && !("else" in jsonSchema))
		// if alone is a valid no-op
		return type.unknown

	const thenSchema =
		"then" in jsonSchema ?
			jsonSchemaToType(jsonSchema.then as JsonSchemaOrBoolean)
		:	undefined
	const elseSchema =
		"else" in jsonSchema ?
			jsonSchemaToType(jsonSchema.else as JsonSchemaOrBoolean)
		:	undefined

	const jsonSchemaIfThenElseValidator = (
		data: unknown,
		ctx: Traversal
	): boolean => {
		if (ifSchema.allows(data))
			return thenSchema === undefined ? true : applyBranch(thenSchema, data, ctx)
		return elseSchema === undefined ? true : applyBranch(elseSchema, data, ctx)
	}

	return type.unknown.narrow(jsonSchemaIfThenElseValidator)
}

import type { Traversal } from "@ark/schema"
import { printable, throwParseError } from "@ark/util"
import { type JsonSchema, type Type, type } from "arktype"
import { writeJsonSchemaCommonConstAndEnumMessage } from "./errors.ts"

const deepNormalize = (data: unknown): unknown =>
	typeof data === "object" ?
		data === null ? null
		: Array.isArray(data) ? data.map(item => deepNormalize(item))
		: Object.fromEntries(
				Object.entries(data)
					.map(([k, v]) => [k, deepNormalize(v)] as const)
					.sort((l, r) => (l[0] > r[0] ? 1 : -1))
			)
	:	data

const jsonValuesEqual = (l: unknown, r: unknown): boolean =>
	JSON.stringify(deepNormalize(l)) === JSON.stringify(deepNormalize(r))

export const parseCommonJsonSchema = (
	jsonSchema: JsonSchema
): Type | undefined => {
	if ("const" in jsonSchema) {
		if ("enum" in jsonSchema)
			throwParseError(writeJsonSchemaCommonConstAndEnumMessage())

		return type.unit(jsonSchema.const)
	}

	if ("enum" in jsonSchema) {
		const values = jsonSchema.enum
		if (values.length === 0) return type.never

		const hasStructuredValue = values.some(
			value => typeof value === "object" && value !== null
		)
		if (!hasStructuredValue) return type.enumerated(...values)

		const jsonSchemaEnumValidator = (data: unknown, ctx: Traversal) => {
			if (values.some(value => jsonValuesEqual(value, data))) return true
			return ctx.reject({
				expected: `one of ${printable(values)}`,
				actual: printable(data)
			})
		}
		return type.unknown.narrow(jsonSchemaEnumValidator)
	}
}

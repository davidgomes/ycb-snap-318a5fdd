import type { JsonSchemaOrBoolean } from "@ark/schema"
import { throwParseError } from "@ark/util"
import { type, type JsonSchema, type Type } from "arktype"
import {
	writeJsonSchemaUnresolvedRefMessage,
	writeJsonSchemaUnsupportedRefMessage
} from "./errors.ts"

const localDefRefPattern = /^#\/\$defs\/([^/]+)$/

type RefParseContext = {
	defs: Record<string, JsonSchema>
	resolved: Map<string, Type>
	placeholders: Map<string, Type>
	parse: (schema: JsonSchemaOrBoolean) => Type
}

let ctx: RefParseContext | undefined

const extractRootDefs = (
	schema: JsonSchemaOrBoolean
): Record<string, JsonSchema> => {
	if (typeof schema !== "object" || schema === null || Array.isArray(schema))
		return {}
	if (!("$defs" in schema) || schema.$defs === undefined) return {}
	return schema.$defs
}

export const withJsonSchemaRefContext = <t>(
	schema: JsonSchemaOrBoolean,
	parse: (schema: JsonSchemaOrBoolean) => Type,
	fn: () => t
): t => {
	if (ctx !== undefined) return fn()

	ctx = {
		defs: extractRootDefs(schema),
		resolved: new Map(),
		placeholders: new Map(),
		parse
	}
	try {
		return fn()
	} finally {
		ctx = undefined
	}
}

export const fullyResolveJsonSchemaType = (t: Type): Type => {
	let current = t
	const seen = new Set<unknown>()
	while (current.internal.hasKind("alias")) {
		if (seen.has(current.internal)) break
		seen.add(current.internal)

		const reference = current.internal.reference
		const cached = ctx?.resolved.get(reference)
		if (cached !== undefined) {
			current = cached
			continue
		}

		// Resolution is not ready yet (recursive parse in progress).
		// Leave the alias in place so composition does not unwrap a stub.
		break
	}
	return current
}

export const resolveJsonSchemaRef = (ref: unknown): Type => {
	if (typeof ref !== "string" || !localDefRefPattern.test(ref)) {
		throwParseError(writeJsonSchemaUnsupportedRefMessage())
	}

	const name = ref.slice("#/$defs/".length)
	if (ctx === undefined || !(name in ctx.defs))
		throwParseError(writeJsonSchemaUnresolvedRefMessage(ref))

	const completed = ctx.resolved.get(name)
	if (completed !== undefined) return fullyResolveJsonSchemaType(completed)

	const existingPlaceholder = ctx.placeholders.get(name)
	if (existingPlaceholder !== undefined) return existingPlaceholder

	const placeholder = type.schema({
		reference: name,
		resolve: () => {
			const resolved = ctx?.resolved.get(name)
			if (resolved === undefined) return placeholder.internal
			return fullyResolveJsonSchemaType(resolved).internal
		}
	}) as Type

	ctx.placeholders.set(name, placeholder)
	const parsed = ctx.parse(ctx.defs[name])
	ctx.resolved.set(name, parsed)
	return fullyResolveJsonSchemaType(parsed)
}

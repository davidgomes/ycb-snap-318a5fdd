import type { JsonSchemaOrBoolean, Traversal } from "@ark/schema"
import { printable, throwParseError } from "@ark/util"
import { type, type Type } from "arktype"
import {
	writeJsonSchemaUnresolvedRefMessage,
	writeJsonSchemaUnsupportedRefMessage
} from "./errors.ts"

const localDefRefPattern = /^#\/\$defs\/([^/]+)$/

type RefParseContext = {
	defs: Record<string, JsonSchemaOrBoolean>
	resolved: Map<string, Type>
	placeholders: Map<string, Type>
	resolving: Set<string>
	parse: (schema: JsonSchemaOrBoolean) => Type
}

let ctx: RefParseContext | undefined

const extractRootDefs = (
	schema: JsonSchemaOrBoolean
): Record<string, JsonSchemaOrBoolean> => {
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
		resolving: new Set(),
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
		const cached = ctx?.resolved.get(current.internal.reference)
		if (cached === undefined) break
		current = cached
	}
	return current
}

const deferredRef = (name: string): Type => {
	const owner = ctx!
	const existing = owner.placeholders.get(name)
	if (existing !== undefined) return existing

	const jsonSchemaRefValidator = (data: unknown, traversal: Traversal) => {
		const resolved = owner.resolved.get(name)
		if (resolved === undefined) {
			return traversal.reject({
				expected: `a value matching $ref "#/$defs/${name}"`,
				actual: printable(data)
			})
		}
		if (resolved.allows(data)) return true
		return traversal.reject({
			expected: resolved.description,
			actual: printable(data)
		})
	}

	const placeholder = type.unknown.narrow(jsonSchemaRefValidator)
	owner.placeholders.set(name, placeholder)
	return placeholder
}

export const resolveJsonSchemaRef = (ref: unknown): Type => {
	if (typeof ref !== "string" || !localDefRefPattern.test(ref))
		throwParseError(writeJsonSchemaUnsupportedRefMessage())

	const name = ref.slice("#/$defs/".length)
	if (ctx === undefined || !(name in ctx.defs))
		throwParseError(writeJsonSchemaUnresolvedRefMessage(ref))

	const completed = ctx.resolved.get(name)
	if (completed !== undefined) return fullyResolveJsonSchemaType(completed)

	if (ctx.resolving.has(name)) return deferredRef(name)

	ctx.resolving.add(name)
	try {
		const parsed = ctx.parse(ctx.defs[name])
		ctx.resolved.set(name, parsed)
		return fullyResolveJsonSchemaType(parsed)
	} finally {
		ctx.resolving.delete(name)
	}
}

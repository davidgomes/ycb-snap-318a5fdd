import { attest, contextualize } from "@ark/attest"
import {
	jsonSchemaToType,
	writeJsonSchemaUnresolvedRefMessage,
	writeJsonSchemaUnsupportedRefMessage
} from "@ark/json-schema"

contextualize(() => {
	it("resolves a local $defs ref", () => {
		const t = jsonSchemaToType({
			$defs: {
				NonEmptyString: { type: "string", minLength: 1 }
			},
			$ref: "#/$defs/NonEmptyString"
		})
		attest(t.allows("a")).equals(true)
		attest(t.allows("")).equals(false)
		attest(t.allows(1)).equals(false)
	})

	it("supports recursive $ref", () => {
		const t = jsonSchemaToType({
			$defs: {
				Node: {
					type: "object",
					properties: {
						value: { type: "string" },
						next: { $ref: "#/$defs/Node" }
					},
					required: ["value"]
				}
			},
			$ref: "#/$defs/Node"
		})
		attest(t.allows({ value: "a" })).equals(true)
		attest(
			t.allows({ value: "a", next: { value: "b", next: { value: "c" } } })
		).equals(true)
		attest(t.allows({ value: "a", next: { value: 1 } })).equals(false)
	})

	it("supports $ref inside anyOf without wrapping twice", () => {
		const t = jsonSchemaToType({
			$defs: {
				Str: { type: "string" }
			},
			anyOf: [{ $ref: "#/$defs/Str" }, { type: "number" }]
		})
		attest(t.allows("ok")).equals(true)
		attest(t.allows(1)).equals(true)
		attest(t.allows(true)).equals(false)
	})

	it("supports recursive $ref inside anyOf", () => {
		const t = jsonSchemaToType({
			$defs: {
				Value: {
					anyOf: [
						{ type: "string" },
						{
							type: "object",
							properties: {
								child: { $ref: "#/$defs/Value" }
							},
							required: ["child"]
						}
					]
				}
			},
			$ref: "#/$defs/Value"
		})
		attest(t.allows("leaf")).equals(true)
		attest(t.allows({ child: "leaf" })).equals(true)
		attest(t.allows({ child: { child: "leaf" } })).equals(true)
		attest(t.allows({ child: 1 })).equals(false)
	})

	it("supports $ref in dependentSchemas", () => {
		const t = jsonSchemaToType({
			$defs: {
				NeedsBar: {
					type: "object",
					properties: {
						bar: { type: "number" }
					},
					required: ["bar"]
				}
			},
			type: "object",
			dependentSchemas: {
				foo: { $ref: "#/$defs/NeedsBar" }
			}
		})
		attest(t.allows({})).equals(true)
		attest(t.allows({ foo: true, bar: 1 })).equals(true)
		attest(t.allows({ foo: true })).equals(false)
	})

	it("rejects unsupported $ref formats", () => {
		// @ts-expect-error invalid ref format is checked at runtime
		attest(() => jsonSchemaToType({ $ref: "#/definitions/Foo" })).throws(
			writeJsonSchemaUnsupportedRefMessage()
		)
		attest(() =>
			// @ts-expect-error invalid ref format is checked at runtime
			jsonSchemaToType({ $ref: "https://example.com/schema" })
		).throws(writeJsonSchemaUnsupportedRefMessage())
	})

	it("rejects missing $defs names", () => {
		attest(() =>
			jsonSchemaToType({
				$defs: { Existing: { type: "string" } },
				$ref: "#/$defs/NonExistentDef"
			})
		).throws(writeJsonSchemaUnresolvedRefMessage("#/$defs/NonExistentDef"))
	})
})

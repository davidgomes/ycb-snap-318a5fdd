import { attest, contextualize } from "@ark/attest"
import { jsonSchemaToType } from "@ark/json-schema"

contextualize(() => {
	it("applies then when if matches", () => {
		const t = jsonSchemaToType({
			if: { type: "string" },
			then: { type: "string", minLength: 3 }
		})
		attest(t.allows("foo")).equals(true)
		attest(t.allows("fo")).equals(false)
		attest(t.allows(1)).equals(true)
	})

	it("applies else when if does not match", () => {
		const t = jsonSchemaToType({
			if: { type: "string" },
			then: { type: "string", minLength: 2 },
			else: { type: "number", minimum: 0 }
		})
		attest(t.allows("ab")).equals(true)
		attest(t.allows("a")).equals(false)
		attest(t.allows(1)).equals(true)
		attest(t.allows(-1)).equals(false)
	})

	it("if alone is a no-op", () => {
		const t = jsonSchemaToType({ if: { type: "string" } })
		attest(t.allows("x")).equals(true)
		attest(t.allows(1)).equals(true)
		attest(t.allows(null)).equals(true)
	})

	it("then/else without if are ignored", () => {
		const t = jsonSchemaToType({
			type: "number",
			then: { const: 1 },
			else: { const: 2 }
		})
		attest(t.allows(1)).equals(true)
		attest(t.allows(2)).equals(true)
		attest(t.allows(3)).equals(true)
	})

	it("applies to non-object values", () => {
		const t = jsonSchemaToType({
			if: { const: 5 },
			then: { type: "integer" }
		})
		attest(t.allows(5)).equals(true)
		attest(t.allows("5")).equals(true)
	})

	it("nests if/then/else", () => {
		const t = jsonSchemaToType({
			if: { type: "number" },
			then: {
				if: { minimum: 10 },
				then: { multipleOf: 2 },
				else: { maximum: 3 }
			}
		})
		attest(t.allows(12)).equals(true)
		attest(t.allows(11)).equals(false)
		attest(t.allows(2)).equals(true)
		attest(t.allows(4)).equals(false)
		attest(t.allows("ok")).equals(true)
	})

	it("combines with type and properties", () => {
		const t = jsonSchemaToType({
			type: "object",
			properties: {
				flag: { type: "boolean" },
				name: { type: "string" }
			},
			if: {
				properties: { flag: { const: true } },
				required: ["flag"]
			},
			then: { required: ["name"] }
		})
		attest(t.allows({})).equals(true)
		attest(t.allows({ flag: false })).equals(true)
		attest(t.allows({ flag: true, name: "Ada" })).equals(true)
		attest(t.allows({ flag: true })).equals(false)
	})

	it("chains conditions via allOf", () => {
		const t = jsonSchemaToType({
			allOf: [
				{
					if: { const: "a" },
					then: { type: "string", minLength: 1 }
				},
				{
					if: { type: "number" },
					then: { minimum: 0 }
				}
			]
		})
		attest(t.allows("a")).equals(true)
		attest(t.allows(1)).equals(true)
		attest(t.allows(-1)).equals(false)
	})

	it("supports $ref in if/then/else", () => {
		const t = jsonSchemaToType({
			$defs: {
				Pos: { type: "number", minimum: 0 }
			},
			if: { type: "number" },
			then: { $ref: "#/$defs/Pos" }
		})
		attest(t.allows(1)).equals(true)
		attest(t.allows(-1)).equals(false)
		attest(t.allows("x")).equals(true)
	})

	it("supports boolean if schemas", () => {
		const alwaysThen = jsonSchemaToType({
			if: true,
			then: { const: 1 }
		})
		attest(alwaysThen.allows(1)).equals(true)
		attest(alwaysThen.allows(2)).equals(false)

		const neverThen = jsonSchemaToType({
			if: false,
			then: { const: 1 },
			else: { const: 2 }
		})
		attest(neverThen.allows(1)).equals(false)
		attest(neverThen.allows(2)).equals(true)
	})

	it("treats then/else object keywords without type as objects", () => {
		const t = jsonSchemaToType({
			if: { type: "object" },
			then: {
				properties: { name: { type: "string" } },
				required: ["name"]
			}
		})
		attest(t.allows(false)).equals(true)
		attest(t.allows({ name: "Ada" })).equals(true)
		attest(t.allows({})).equals(false)
	})
})

import { attest, contextualize } from "@ark/attest"
import { jsonSchemaToType } from "@ark/json-schema"

contextualize(() => {
	it("enum matches primitives", () => {
		const t = jsonSchemaToType({ enum: ["red", 1, null] })
		attest(t.allows("red")).equals(true)
		attest(t.allows(1)).equals(true)
		attest(t.allows(null)).equals(true)
		attest(t.allows("blue")).equals(false)
	})

	it("enum uses deep equality for objects and arrays", () => {
		const t = jsonSchemaToType({
			enum: [{ a: 1, b: [2, { c: 3 }] }, ["x", { y: true }]]
		})
		attest(t.allows({ a: 1, b: [2, { c: 3 }] })).equals(true)
		attest(t.allows({ b: [2, { c: 3 }], a: 1 })).equals(true)
		attest(t.allows(["x", { y: true }])).equals(true)
		attest(t.allows({ a: 1, b: [2, { c: 4 }] })).equals(false)
		attest(t.allows(["x", { y: false }])).equals(false)
		attest(t.allows({ a: 1 })).equals(false)
	})
})

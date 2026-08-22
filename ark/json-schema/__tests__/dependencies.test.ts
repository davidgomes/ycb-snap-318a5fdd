import { attest, contextualize } from "@ark/attest"
import { jsonSchemaToType } from "@ark/json-schema"

contextualize(() => {
	it("dependentRequired requires keys when the trigger is present", () => {
		const t = jsonSchemaToType({
			type: "object",
			dependentRequired: {
				credit_card: ["billing_address"]
			}
		})
		attest(t.allows({})).equals(true)
		attest(t.allows({ billing_address: "1 Main" })).equals(true)
		attest(t.allows({ credit_card: "5555", billing_address: "1 Main" })).equals(
			true
		)
		attest(t.allows({ credit_card: "5555" })).equals(false)
	})

	it("dependentSchemas validates when the trigger is present", () => {
		const t = jsonSchemaToType({
			type: "object",
			dependentSchemas: {
				credit_card: {
					properties: {
						billing_address: { type: "string" }
					},
					required: ["billing_address"]
				}
			}
		})
		attest(t.allows({})).equals(true)
		attest(t.allows({ credit_card: 1, billing_address: "1 Main" })).equals(true)
		attest(t.allows({ credit_card: 1 })).equals(false)
	})

	it("draft-07 dependencies supports property and schema forms", () => {
		const requiredForm = jsonSchemaToType({
			type: "object",
			dependencies: {
				foo: ["bar"]
			}
		})
		attest(requiredForm.allows({ foo: 1, bar: 2 })).equals(true)
		attest(requiredForm.allows({ foo: 1 })).equals(false)

		const schemaForm = jsonSchemaToType({
			type: "object",
			dependencies: {
				foo: {
					properties: { bar: { type: "number" } },
					required: ["bar"]
				}
			}
		})
		attest(schemaForm.allows({ foo: 1, bar: 2 })).equals(true)
		attest(schemaForm.allows({ foo: 1 })).equals(false)
	})

	it("implicit object schemas accept dependency keywords without type", () => {
		const t = jsonSchemaToType({
			dependentRequired: { a: ["b"] }
		})
		attest(t.allows({})).equals(true)
		attest(t.allows({ a: 1, b: 2 })).equals(true)
		attest(t.allows({ a: 1 })).equals(false)
	})
})

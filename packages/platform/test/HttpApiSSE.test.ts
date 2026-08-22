import { HttpApiSSE } from "@effect/platform"
import { assert, describe, it } from "@effect/vitest"
import { Effect, Schema } from "effect"

class MessageCreated extends Schema.TaggedClass<MessageCreated>("MessageCreated")("MessageCreated", {
  id: Schema.Number,
  text: Schema.String
}) {}

class MessageDeleted extends Schema.TaggedClass<MessageDeleted>("MessageDeleted")("MessageDeleted", {
  id: Schema.Number
}) {}

const Event = Schema.Union(MessageCreated, MessageDeleted)

describe("HttpApiSSE", () => {
  it("formatMessage supports multi-line data", () => {
    assert.strictEqual(
      HttpApiSSE.formatMessage({ data: "line1\nline2", event: "test" }),
      "event: test\ndata: line1\ndata: line2\n\n"
    )
  })

  it("formatDataMessage JSON-encodes values", () => {
    assert.strictEqual(
      HttpApiSSE.formatDataMessage({ a: 1 }),
      "data: {\"a\":1}\n\n"
    )
  })

  it("makeUnionEventEncoder sets event from _tag", () =>
    Effect.gen(function*() {
      const encoder = HttpApiSSE.makeUnionEventEncoder(Event)
      const message = yield* encoder(new MessageCreated({ id: 1, text: "hello" }))
      assert.include(message, "event: MessageCreated\n")
      assert.include(message, "\"text\":\"hello\"")
    }))

  it("makeEventEncoder emits data-only messages for non-unions", () =>
    Effect.gen(function*() {
      const encoder = HttpApiSSE.makeEventEncoder(Schema.Struct({ value: Schema.String }))
      const message = yield* encoder({ value: "hello" })
      assert.strictEqual(message, "data: {\"value\":\"hello\"}\n\n")
    }))

  it("makeUnionEventDecoder decodes SSE messages", () =>
    Effect.gen(function*() {
      const decoder = HttpApiSSE.makeUnionEventDecoder(Event)
      const created = yield* decoder({
        event: "MessageCreated",
        data: JSON.stringify({ id: 1, text: "hello" })
      })
      assert.deepStrictEqual(created, new MessageCreated({ id: 1, text: "hello" }))
    }))

  it("makeEventDecoder decodes JSON data", () =>
    Effect.gen(function*() {
      const decoder = HttpApiSSE.makeEventDecoder(Schema.Struct({ value: Schema.String }))
      const value = yield* decoder(JSON.stringify({ value: "hello" }))
      assert.deepStrictEqual(value, { value: "hello" })
    }))
})

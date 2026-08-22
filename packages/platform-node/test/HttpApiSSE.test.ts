import { HttpApi, HttpApiBuilder, HttpApiClient, HttpApiEndpoint, HttpApiGroup, HttpApiSchema } from "@effect/platform"
import { NodeHttpServer } from "@effect/platform-node"
import { assert, describe, it } from "@effect/vitest"
import { Chunk, Effect, Layer, Schema, Stream } from "effect"

class MessageCreated extends Schema.TaggedClass<MessageCreated>("MessageCreated")("MessageCreated", {
  id: Schema.Number,
  text: Schema.String
}) {}

class MessageDeleted extends Schema.TaggedClass<MessageDeleted>("MessageDeleted")("MessageDeleted", {
  id: Schema.Number
}) {}

const Event = Schema.Union(MessageCreated, MessageDeleted)

const Api = HttpApi.make("api").add(
  HttpApiGroup.make("events")
    .add(HttpApiEndpoint.sse("stream", "/events").addSuccess(HttpApiSchema.withSSE(Event)))
    .add(HttpApiEndpoint.sse("auto", "/auto").addSuccess(HttpApiSchema.withSSE(Event)))
)

const HttpEventsLive = HttpApiBuilder.group(Api, "events", (handlers) =>
  handlers
    .handleStream("stream", () =>
      Stream.make(
        new MessageCreated({ id: 1, text: "hello" }),
        new MessageDeleted({ id: 1 })
      ))
    .handle("auto", () => Stream.make(new MessageCreated({ id: 2, text: "world" }))))

const HttpLive = HttpApiBuilder.serve().pipe(
  Layer.provide(HttpApiBuilder.middlewareCors()),
  Layer.provide(Layer.provide(HttpApiBuilder.api(Api), HttpEventsLive)),
  Layer.provideMerge(NodeHttpServer.layerTest)
)

describe("HttpApi SSE integration", () => {
  it.effect("handleStream serves typed SSE events", () =>
    Effect.gen(function*() {
      const client = yield* HttpApiClient.make(Api)
      const stream = yield* client.events.stream({})
      const events = yield* Stream.runCollect(stream)
      assert.strictEqual(Chunk.size(events), 2)
      assert.deepStrictEqual(Chunk.unsafeGet(events, 0), new MessageCreated({ id: 1, text: "hello" }))
      assert.deepStrictEqual(Chunk.unsafeGet(events, 1), new MessageDeleted({ id: 1 }))
    }).pipe(Effect.provide(HttpLive)))

  it.effect("handle auto-detects Stream responses on sse endpoints", () =>
    Effect.gen(function*() {
      const client = yield* HttpApiClient.make(Api)
      const stream = yield* client.events.auto({})
      const events = yield* Stream.runCollect(stream)
      assert.strictEqual(Chunk.size(events), 1)
      assert.deepStrictEqual(Chunk.unsafeGet(events, 0), new MessageCreated({ id: 2, text: "world" }))
    }).pipe(Effect.provide(HttpLive)))

  it("isSSE guard identifies sse endpoints", () => {
    assert.isTrue(HttpApiEndpoint.isSSE(HttpApiEndpoint.sse("events", "/events")))
    assert.isFalse(HttpApiEndpoint.isSSE(HttpApiEndpoint.get("events", "/events")))
    assert.isFalse(HttpApiSchema.getSSE(Schema.String.ast))
  })
})

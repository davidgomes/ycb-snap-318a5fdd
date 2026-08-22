/**
 * @since 1.0.0
 */
import * as Effect from "effect/Effect"
import * as ParseResult from "effect/ParseResult"
import * as Schema from "effect/Schema"
import type * as AST from "effect/SchemaAST"
import * as Stream from "effect/Stream"
import type * as HttpClientError from "./HttpClientError.js"
import type * as HttpClientResponse from "./HttpClientResponse.js"
import * as HttpServerResponse from "./HttpServerResponse.js"

/**
 * @since 1.0.0
 * @category models
 */
export interface SSEMessage {
  readonly data: string
  readonly event?: string | undefined
  readonly id?: string | undefined
  readonly retry?: number | undefined
}

/**
 * @since 1.0.0
 * @category encoding
 */
export const formatMessage = (msg: SSEMessage): string => {
  let out = ""
  if (msg.id !== undefined) {
    out += `id: ${msg.id}\n`
  }
  if (msg.event !== undefined) {
    out += `event: ${msg.event}\n`
  }
  if (msg.retry !== undefined) {
    out += `retry: ${msg.retry}\n`
  }
  if (msg.data !== "") {
    out += `data: ${msg.data.replace(/\n/g, "\ndata: ")}\n`
  }
  return out + "\n"
}

/**
 * @since 1.0.0
 * @category encoding
 */
export const formatDataMessage = (data: unknown): string => formatMessage({ data: JSON.stringify(data) })

const unwrapAst = (ast: AST.AST): AST.AST => {
  switch (ast._tag) {
    case "Transformation":
    case "Refinement":
      return unwrapAst(ast.from)
    case "Suspend":
      return unwrapAst(ast.f())
    default:
      return ast
  }
}

const isUnionSchema = (schema: Schema.Schema.Any): boolean => unwrapAst(schema.ast)._tag === "Union"

const jsonParseError = (value: unknown): ParseResult.ParseError =>
  ParseResult.parseError(new ParseResult.Type(Schema.String.ast, value, "Could not parse JSON"))

/**
 * @since 1.0.0
 * @category encoding
 */
export const makeEventEncoder = <A extends Schema.Schema.Any>(
  schema: A
): (value: Schema.Schema.Type<A>) => Effect.Effect<string, ParseResult.ParseError, unknown> => {
  const encode = Schema.encodeUnknown(schema)
  return (value) =>
    Effect.map(
      encode(value),
      (encoded) => formatMessage({ data: JSON.stringify(encoded) })
    )
}

/**
 * @since 1.0.0
 * @category encoding
 */
export const makeUnionEventEncoder = <A extends Schema.Schema.Any>(
  schema: A
): (value: Schema.Schema.Type<A>) => Effect.Effect<string, ParseResult.ParseError, unknown> => {
  if (!isUnionSchema(schema)) {
    return makeEventEncoder(schema)
  }
  const encode = Schema.encodeUnknown(schema)
  return (value) =>
    Effect.map(encode(value), (encoded) => {
      const event = typeof encoded === "object" && encoded !== null && "_tag" in encoded
        ? String((encoded as { readonly _tag: string })._tag)
        : undefined
      return formatMessage({
        data: JSON.stringify(encoded),
        event
      })
    })
}

/**
 * @since 1.0.0
 * @category decoding
 */
export const makeEventDecoder = <A extends Schema.Schema.Any>(
  schema: A
): (data: string) => Effect.Effect<Schema.Schema.Type<A>, ParseResult.ParseError, unknown> => {
  const decode = Schema.decodeUnknown(Schema.parseJson(schema))
  return (data) => data === "" ? Effect.fail(jsonParseError(data)) : decode(data)
}

/**
 * @since 1.0.0
 * @category decoding
 */
export const makeUnionEventDecoder = <A extends Schema.Schema.Any>(
  schema: A
): (message: SSEMessage) => Effect.Effect<Schema.Schema.Type<A>, ParseResult.ParseError, unknown> => {
  if (!isUnionSchema(schema)) {
    return (message) => makeEventDecoder(schema)(message.data)
  }
  const decode = Schema.decodeUnknown(schema)
  return (message) => {
    if (message.data === "") {
      return Effect.fail(jsonParseError(message))
    }
    return Effect.flatMap(
      Effect.try({
        try: () => JSON.parse(message.data) as Record<string, unknown>,
        catch: () => jsonParseError(message)
      }),
      (parsed) => {
        const value = message.event !== undefined && !("_tag" in parsed)
          ? { ...parsed, _tag: message.event }
          : parsed
        return decode(value)
      }
    )
  }
}

/**
 * @since 1.0.0
 * @category streams
 */
export const fromStream = <A, E, R>(
  stream: Stream.Stream<A, E, R>,
  encoder: (value: A) => Effect.Effect<string, ParseResult.ParseError, R>
): Stream.Stream<Uint8Array, E | ParseResult.ParseError, R> =>
  stream.pipe(
    Stream.mapEffect(encoder),
    Stream.encodeText
  )

/**
 * @since 1.0.0
 * @category streams
 */
export const toResponse = (
  stream: Stream.Stream<any, any, any>,
  encoder: (value: any) => Effect.Effect<string, ParseResult.ParseError, unknown>,
  options?: HttpServerResponse.Options | undefined
): Effect.Effect<HttpServerResponse.HttpServerResponse> =>
  Effect.sync(() =>
    HttpServerResponse.stream(fromStream(stream, encoder) as Stream.Stream<Uint8Array, any, never>, {
      ...options,
      headers: {
        "cache-control": "no-cache",
        connection: "keep-alive",
        ...options?.headers
      },
      contentType: "text/event-stream"
    })
  )

const parseMessages = (buffer: string): { readonly messages: ReadonlyArray<SSEMessage>; readonly rest: string } => {
  const messages: Array<SSEMessage> = []
  let index = 0
  while (index < buffer.length) {
    const boundary = buffer.indexOf("\n\n", index)
    if (boundary === -1) {
      break
    }
    const block = buffer.slice(index, boundary)
    index = boundary + 2
    if (block.length === 0) {
      continue
    }
    let data = ""
    let event: string | undefined
    let id: string | undefined
    let retry: number | undefined
    for (const line of block.split("\n")) {
      if (line.startsWith(":")) {
        continue
      }
      const colon = line.indexOf(":")
      const field = colon === -1 ? line : line.slice(0, colon)
      const value = colon === -1 ? "" : line.slice(colon + 1).replace(/^\s/, "")
      switch (field) {
        case "data":
          data += data === "" ? value : `\n${value}`
          break
        case "event":
          event = value
          break
        case "id":
          id = value
          break
        case "retry": {
          const parsed = Number.parseInt(value, 10)
          if (!Number.isNaN(parsed)) {
            retry = parsed
          }
          break
        }
      }
    }
    messages.push({ data, event, id, retry })
  }
  return { messages, rest: buffer.slice(index) }
}

/**
 * @since 1.0.0
 * @category streams
 */
export const toStream = <A, E, R>(
  response: HttpClientResponse.HttpClientResponse,
  decoder: (message: SSEMessage) => Effect.Effect<A, E, R>
): Stream.Stream<A, E | HttpClientError.HttpClientError | ParseResult.ParseError, R> => {
  let buffer = ""
  return response.stream.pipe(
    Stream.map((chunk) => new TextDecoder().decode(chunk)),
    Stream.flatMap((chunk) => {
      buffer += chunk
      const parsed = parseMessages(buffer)
      buffer = parsed.rest
      return Stream.fromIterable(parsed.messages)
    }),
    Stream.mapEffect(decoder)
  )
}

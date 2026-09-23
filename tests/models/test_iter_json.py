import gzip
import json
import typing

import pytest

import httpx


def _response(
    payload: bytes,
    content_type: str | None,
    *,
    chunk_size: int | None = None,
    headers: dict[str, str] | None = None,
    request: httpx.Request | None = None,
) -> httpx.Response:
    merged: dict[str, str] = dict(headers or {})
    if content_type is not None:
        merged["Content-Type"] = content_type
    if chunk_size is None:
        return httpx.Response(200, content=payload, headers=merged, request=request)

    def chunks() -> typing.Iterator[bytes]:
        if not payload:
            return
        for index in range(0, len(payload), chunk_size):
            yield payload[index : index + chunk_size]

    return httpx.Response(200, content=chunks(), headers=merged, request=request)


def _values(
    payload: bytes,
    content_type: str,
    *,
    chunk_size: int | None = None,
    **kwargs: typing.Any,
) -> list[typing.Any]:
    response = _response(payload, content_type, chunk_size=chunk_size)
    return list(response.iter_json(**kwargs))


def _assert_error(
    payload: bytes,
    content_type: str | None,
    *,
    chunk_size: int | None = None,
    headers: dict[str, str] | None = None,
) -> None:
    response = _response(
        payload, content_type, chunk_size=chunk_size, headers=headers
    )
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())


@pytest.mark.parametrize(
    "content_type",
    [
        "application/json",
        "Application/JSON",
        "application/json; charset=utf-8",
        "application/json;charset=utf-8",
        "application/json; charset=utf-8; foo=bar",
        "application/json;",
        "application/vnd.api+json",
        "application/problem+json; charset=utf-8",
        "application/geo+json",
        "Application/Vnd.API+JSON",
        "application/a+json",
        "application/a+b+json",
    ],
)
def test_json_document_content_types(content_type: str) -> None:
    payload = b'[1, {"a": 2}, [3], "x", true, false, null]'
    assert _values(payload, content_type) == [1, {"a": 2}, [3], "x", True, False, None]


@pytest.mark.parametrize(
    "content_type",
    [
        "application/ndjson",
        "application/x-ndjson",
        "Application/NDJSON",
        "Application/X-NDJSON",
        "application/ndjson; charset=utf-8",
        "application/x-ndjson;charset=utf-8; profile=stream",
    ],
)
def test_ndjson_content_types(content_type: str) -> None:
    payload = b'{"a":1}\n[2,3]\n"x"\n'
    assert _values(payload, content_type) == [{"a": 1}, [2, 3], "x"]


@pytest.mark.parametrize(
    "content_type",
    [
        "application/json-seq",
        "APPLICATION/JSON-SEQ",
        "application/json-seq; charset=utf-8",
        "application/json-seq ; charset=utf-8 ; foo=bar",
    ],
)
def test_json_seq_content_types(content_type: str) -> None:
    payload = b'\x1e{"a":1}\n\x1e[2,3]\n'
    assert _values(payload, content_type) == [{"a": 1}, [2, 3]]


@pytest.mark.parametrize(
    "content_type",
    [
        None,
        "",
        "   ",
        "text/plain",
        "text/json",
        "image/svg+json",
        "image/svg+json; charset=utf-8",
        "application/xml",
        "application/jsonl",
        "application/jsonlines",
        "application/x-json-stream",
        "application/+json",
        "application/foo+json-seq",
        "application/json-seq+xml",
        "noslash",
    ],
)
def test_rejects_non_json_content_types(content_type: str | None) -> None:
    _assert_error(b"{}", content_type)


@pytest.mark.parametrize(
    "content_type",
    [
        "application/json; charset=not-a-codec",
        "application/json; charset=",
        'application/json; charset=""',
        "application/ndjson; charset=nope",
        "application/json-seq; charset=invalid-codec-name",
        "application/vnd.api+json; charset=definitely-not-real",
    ],
)
def test_rejects_invalid_charset(content_type: str) -> None:
    _assert_error(b"{}", content_type)


def test_invalid_content_type_does_not_consume_stream() -> None:
    started = False

    def body() -> typing.Iterator[bytes]:
        nonlocal started
        started = True
        yield b"{}"

    response = httpx.Response(
        200, content=body(), headers={"Content-Type": "image/svg+json"}
    )
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())
    assert started is False
    assert response.is_stream_consumed is False
    assert response.is_closed is False


def test_invalid_charset_does_not_consume_stream() -> None:
    started = False

    def body() -> typing.Iterator[bytes]:
        nonlocal started
        started = True
        yield b"{}"

    response = httpx.Response(
        200,
        content=body(),
        headers={"Content-Type": "application/json; charset=not-a-codec"},
    )
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())
    assert started is False
    assert response.is_stream_consumed is False


@pytest.mark.parametrize(
    "payload, expected",
    [
        (b"{}", [{}]),
        (b"[]", []),
        (b"[ ]", []),
        (b"null", [None]),
        (b"true", [True]),
        (b"false", [False]),
        (b'"hi"', ["hi"]),
        (b"0", [0]),
        (b"-2", [-2]),
        (b"1.5", [1.5]),
        (b"1e2", [100.0]),
        (b"[1,2,3]", [1, 2, 3]),
        (b"[ 1 , 2 , 3 ]", [1, 2, 3]),
        (b"[\n1,\n2\n]", [1, 2]),
        (b"[[1, 2], [3]]", [[1, 2], [3]]),
        (b'{"a": [1, 2]}', [{"a": [1, 2]}]),
        (b'{"a":1}\n', [{"a": 1}]),
        (b" \t\r\n[1, 2]\r\n", [1, 2]),
        (b'[null, true, false, ""]', [None, True, False, ""]),
        ('{"café": 1}'.encode("utf-8"), [{"café": 1}]),
    ],
)
@pytest.mark.parametrize("chunk_size", [None, 1, 2, 3, 5])
def test_json_document_values(
    payload: bytes, expected: list[typing.Any], chunk_size: int | None
) -> None:
    assert _values(payload, "application/json", chunk_size=chunk_size) == expected


@pytest.mark.parametrize("chunk_size", [None, 1, 2, 4])
@pytest.mark.parametrize(
    "payload",
    [
        b"",
        b"   ",
        b"\n\t\r ",
        "\ufeff".encode("utf-8"),
        b"\xef\xbb\xbf",
        b"\xef\xbb\xbf   \n",
        b"[",
        b"[1",
        b"[1,",
        b"[1,]",
        b"[,1]",
        b"[1,,2]",
        b"[1 2]",
        b"[01]",
        b"{",
        b'{"a":',
        b'{"a":1}{"b":2}',
        b"[1,2][3]",
        b"[1,2] trailing",
        b"truee",
        b"1.",
        b"1e",
        b"1e+",
        b"-",
        b"01",
        b'"unterminated',
        b"hello",
    ],
)
def test_json_document_errors(payload: bytes, chunk_size: int | None) -> None:
    _assert_error(payload, "application/json", chunk_size=chunk_size)


def test_json_document_utf8_bom_and_whitespace() -> None:
    payload = b' \t\xef\xbb\xbf\n {"a": 1} \r\n'
    assert _values(payload, "application/json") == [{"a": 1}]
    assert _values(payload, "application/json; charset=utf-8", chunk_size=1) == [
        {"a": 1}
    ]
    bom_only_then_value = "\ufeff[1, 2]".encode("utf-8")
    assert _values(bom_only_then_value, "application/json", chunk_size=1) == [1, 2]


def test_json_number_split_across_chunks() -> None:
    assert _values(b"[12, 3]", "application/json", chunk_size=1) == [12, 3]
    assert _values(b"[1.5]", "application/json", chunk_size=1) == [1.5]
    assert _values(b"[1e2, -0]", "application/json", chunk_size=1) == [100.0, 0]

    def chunks() -> typing.Iterator[bytes]:
        yield b"[1."
        yield b"5, 1"
        yield b"e2]"

    response = httpx.Response(
        200, content=chunks(), headers={"Content-Type": "application/json"}
    )
    assert list(response.iter_json()) == [1.5, 100.0]


def test_json_string_and_escape_split_across_chunks() -> None:
    payload = '["hello\\nworld", "caf\\u00e9", {"k": "v"}]'.encode("utf-8")
    assert _values(payload, "application/json", chunk_size=1) == [
        "hello\nworld",
        "café",
        {"k": "v"},
    ]


def test_two_json_texts_are_not_ndjson() -> None:
    _assert_error(b'{"a":1}\n{"b":2}\n', "application/json")
    _assert_error(b'{"a":1}\n{"b":2}\n', "application/vnd.api+json", chunk_size=3)


@pytest.mark.parametrize("chunk_size", [None, 1, 2, 5])
def test_ndjson_lines(chunk_size: int | None) -> None:
    payload = b'{"a":1}\n\n  \n[1, 2]\r\n"x"\rtrue\n  4  \n'
    assert _values(payload, "application/ndjson", chunk_size=chunk_size) == [
        {"a": 1},
        [1, 2],
        "x",
        True,
        4,
    ]


@pytest.mark.parametrize("chunk_size", [None, 1, 3])
@pytest.mark.parametrize(
    "payload",
    [
        b'{"a":1}{"b":2}\n',
        b'{"a":1} trailing\n',
        b"truee\n",
        b"01\n",
        b'"nope\n',
        b"[\n",
        b'{"a":1}\n' + "\ufeff".encode("utf-8") + b'{"b":2}\n',
        "\ufeff".encode("utf-8")
        + b'{"a":1}\n'
        + "\ufeff".encode("utf-8")
        + b'{"b":2}\n',
        "  \ufeff{}\n".encode("utf-8"),
    ],
)
def test_ndjson_errors(payload: bytes, chunk_size: int | None) -> None:
    _assert_error(payload, "application/x-ndjson", chunk_size=chunk_size)


def test_ndjson_bom_placement() -> None:
    assert _values(b'\xef\xbb\xbf{"a":1}\n{"b":2}\n', "application/ndjson") == [
        {"a": 1},
        {"b": 2},
    ]
    assert _values(
        b'\xef\xbb\xbf{"a":1}\n{"b":2}\n',
        "application/ndjson; charset=utf-8",
        chunk_size=1,
    ) == [{"a": 1}, {"b": 2}]
    assert _values(b'\xef\xbb\xbf\n{"a":1}\n', "application/ndjson; charset=utf-8") == [
        {"a": 1}
    ]
    assert _values(b'\n\xef\xbb\xbf{"a":1}\n', "application/ndjson") == [{"a": 1}]
    assert _values(b"\n\n", "application/ndjson") == []
    assert _values(b"", "application/x-ndjson") == []
    assert _values(b"   \r\n\r \n", "application/ndjson", chunk_size=1) == []


def test_ndjson_crlf_split_across_chunks() -> None:
    def chunks() -> typing.Iterator[bytes]:
        yield b'{"a":1}\r'
        yield b'\n{"b":2}\n'

    response = httpx.Response(
        200, content=chunks(), headers={"Content-Type": "application/ndjson"}
    )
    assert list(response.iter_json()) == [{"a": 1}, {"b": 2}]


@pytest.mark.parametrize("chunk_size", [None, 1, 2, 4])
def test_json_seq_records(chunk_size: int | None) -> None:
    payload = (
        b'  \r\n\x1e{"a":1}\n'
        b"\x1e\x1e"
        b"\x1e   \n"
        b"\x1e[1, 2]\n\n"
        b'\x1e  "x"  '
        b"\x1e\n"
        b"\x1etrue\r\n"
    )
    # The record ``\x1e\n`` sits between the string record and ``true`` only if
    # it is followed by another RS. Here ``\x1e\n\x1etrue`` is an ignored
    # empty record and then ``true``.
    assert _values(payload, "application/json-seq", chunk_size=chunk_size) == [
        {"a": 1},
        [1, 2],
        "x",
        True,
    ]


def test_json_seq_empty_and_whitespace() -> None:
    for payload in (b"", b"   ", b"\n\r\t ", b"\n\n"):
        assert _values(payload, "application/json-seq") == []
        assert _values(payload, "application/json-seq", chunk_size=1) == []


@pytest.mark.parametrize("chunk_size", [None, 1, 3])
@pytest.mark.parametrize(
    "payload",
    [
        b"\x1e",
        b"\x1e\n",
        b"\x1e   \n",
        b"\x1e   ",
        b"\x1e\n\n",
        b"\x1e\x1e",
        b'\x1e{"a":1}\n\x1e',
        b'\x1e{"a":1}\n\x1e\n',
        b'\x1e{"a":1}\n\x1e   ',
        b'\x1e{"a":1}\n\x1e \n',
        b"{}",
        b" \x1e",
        b"not-a-record",
        b"\x00\x1e1\n",
        b'\x1e{"a":1}{"b":2}\n',
        b"\x1e1 trailing\n",
        b'\x1e{"a":',
        b"\x1etruee\n",
        b"\x1e01\n",
    ],
)
def test_json_seq_errors(payload: bytes, chunk_size: int | None) -> None:
    _assert_error(payload, "application/json-seq", chunk_size=chunk_size)


def test_json_seq_pretty_record_and_ignored_gaps() -> None:
    payload = b'\x1e{\n  "a": 1\n}\n\x1e\x1e{"b": 2}'
    assert _values(payload, "application/json-seq", chunk_size=2) == [
        {"a": 1},
        {"b": 2},
    ]
    assert _values(b"\x1e\x1e1\n", "application/json-seq") == [1]
    assert _values(b"\x1e \n\x1e2", "application/json-seq") == [2]
    assert _values(b"\n\n\x1e3\n", "application/json-seq", chunk_size=1) == [3]


def test_json_seq_array_is_not_expanded() -> None:
    assert _values(b"\x1e[1,2]\n", "application/json-seq") == [[1, 2]]
    assert _values(b"[1,2]\n", "application/ndjson") == [[1, 2]]


@pytest.mark.parametrize(
    "encoding",
    [
        "utf-8",
        "utf-8-sig",
        "utf-16",
        "utf-16-be",
        "utf-16-le",
        "utf-32",
        "utf-32-be",
        "utf-32-le",
    ],
)
@pytest.mark.parametrize("chunk_size", [None, 1, 3])
def test_json_encoding_detection(encoding: str, chunk_size: int | None) -> None:
    document = [{"greeting": "hello", "n": 1, "ok": True}]
    payload = json.dumps(document).encode(encoding)
    assert _values(payload, "application/json", chunk_size=chunk_size) == document
    assert (
        _values(
            payload,
            f"application/json; charset={encoding}",
            chunk_size=chunk_size,
        )
        == document
    )


def test_charset_alias_and_latin1() -> None:
    payload = '{"é": 1}'.encode("latin-1")
    assert _values(payload, "application/json; charset=latin-1") == [{"é": 1}]
    assert _values(payload, "application/json; charset=iso-8859-1", chunk_size=1) == [
        {"é": 1}
    ]
    _assert_error(payload, "application/json")
    ascii_payload = b'["ok"]'
    assert _values(ascii_payload, "application/json; charset=utf8") == ["ok"]


def test_charset_is_not_overridden_by_detection() -> None:
    payload = b'{"a": 1}'
    _assert_error(payload, "application/json; charset=utf-16")
    _assert_error(payload, "application/json; charset=utf-32", chunk_size=1)


def test_ndjson_and_seq_encodings() -> None:
    lines = '{"a":"é"}\n{"b":2}\n'.encode("utf-16")
    assert _values(lines, "application/ndjson", chunk_size=1) == [{"a": "é"}, {"b": 2}]
    assert _values(lines, "application/x-ndjson; charset=utf-16", chunk_size=2) == [
        {"a": "é"},
        {"b": 2},
    ]
    records = '\x1e{"a":"é"}\n\x1e2\n'.encode("utf-16")
    assert _values(records, "application/json-seq", chunk_size=1) == [{"a": "é"}, 2]
    assert _values(records, "application/json-seq; charset=utf-16", chunk_size=3) == [
        {"a": "é"},
        2,
    ]


def test_response_json_constructor() -> None:
    array_response = httpx.Response(200, json=[1, {"a": 2}, None])
    assert list(array_response.iter_json()) == [1, {"a": 2}, None]
    assert list(array_response.iter_json()) == [1, {"a": 2}, None]

    object_response = httpx.Response(200, json={"a": 1})
    assert list(object_response.iter_json()) == [{"a": 1}]


def test_parse_int_and_object_hook() -> None:
    payload = b'[{"n": 1}, 2]'

    def hook(value: dict[str, typing.Any]) -> dict[str, typing.Any]:
        value["hooked"] = True
        return value

    assert _values(payload, "application/json", parse_int=str, object_hook=hook) == [
        {"n": "1", "hooked": True},
        "2",
    ]


def test_in_memory_iteration_is_repeatable() -> None:
    response = httpx.Response(
        200, content=b"[1, 2, 3]", headers={"Content-Type": "application/json"}
    )
    assert list(response.iter_json()) == [1, 2, 3]
    assert list(response.iter_json()) == [1, 2, 3]
    assert response.content == b"[1, 2, 3]"

    empty = httpx.Response(
        200, content=b"   ", headers={"Content-Type": "application/json"}
    )
    with pytest.raises(httpx.DecodingError):
        list(empty.iter_json())
    with pytest.raises(httpx.DecodingError):
        list(empty.iter_json())


def test_streaming_iteration_consumes_and_closes() -> None:
    def body() -> typing.Iterator[bytes]:
        yield b'[{"a":1},'
        yield b'{"b":2}]'

    response = httpx.Response(
        200, content=body(), headers={"Content-Type": "application/json"}
    )
    assert response.is_closed is False
    assert list(response.iter_json()) == [{"a": 1}, {"b": 2}]
    assert response.is_closed is True
    assert response.is_stream_consumed is True
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_json())
    with pytest.raises(httpx.ResponseNotRead):
        response.content  # noqa: B018


def test_streaming_error_closes_and_second_call_is_stream_consumed() -> None:
    def body() -> typing.Iterator[bytes]:
        yield b"   "

    response = httpx.Response(
        200, content=body(), headers={"Content-Type": "application/json"}
    )
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())
    assert response.is_closed is True
    assert response.is_stream_consumed is True
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_json())


def test_streaming_yields_before_the_body_is_finished() -> None:
    pulled = 0

    def body() -> typing.Iterator[bytes]:
        nonlocal pulled
        pulled += 1
        yield b'[{"a":1},'
        pulled += 1
        yield b'{"b":2}]'

    response = httpx.Response(
        200, content=body(), headers={"Content-Type": "application/vnd.api+json"}
    )
    iterator = iter(response.iter_json())
    assert next(iterator) == {"a": 1}
    assert pulled == 1
    assert response.is_closed is False
    assert next(iterator) == {"b": 2}
    assert pulled == 2
    with pytest.raises(StopIteration):
        next(iterator)
    assert response.is_closed is True


def test_partial_iteration_closes_the_response() -> None:
    def body() -> typing.Iterator[bytes]:
        yield b"[1,"
        yield b"2,3,4]"

    response = httpx.Response(
        200, content=body(), headers={"Content-Type": "application/json"}
    )
    iterator = response.iter_json()
    assert next(iterator) == 1
    assert response.is_stream_consumed is True
    assert response.is_closed is False
    iterator.close()
    assert response.is_closed is True
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_json())


def test_decoding_error_is_associated_with_the_request() -> None:
    request = httpx.Request("GET", "https://example.org/items")
    response = httpx.Response(
        200,
        content=b"",
        headers={"Content-Type": "application/json"},
        request=request,
    )
    with pytest.raises(httpx.DecodingError) as exc_info:
        list(response.iter_json())
    assert exc_info.value.request is request

    typed = httpx.Response(
        200,
        content=b"{}",
        headers={"Content-Type": "text/plain"},
        request=request,
    )
    with pytest.raises(httpx.DecodingError) as exc_info:
        list(typed.iter_json())
    assert exc_info.value.request is request


def test_gzip_encoded_json() -> None:
    payload = b"[1,2,3]"
    compressed = gzip.compress(payload)
    response = httpx.Response(
        200,
        content=compressed,
        headers={"Content-Type": "application/json", "Content-Encoding": "gzip"},
    )
    assert list(response.iter_json()) == [1, 2, 3]

    def chunks() -> typing.Iterator[bytes]:
        midpoint = max(1, len(compressed) // 2)
        yield compressed[:midpoint]
        yield compressed[midpoint:]

    streaming = httpx.Response(
        200,
        content=chunks(),
        headers={"Content-Type": "application/ndjson", "Content-Encoding": "gzip"},
    )
    # One compressed JSON array is a single NDJSON line, so the array is one value.
    assert list(streaming.iter_json()) == [[1, 2, 3]]

    def json_chunks() -> typing.Iterator[bytes]:
        midpoint = max(1, len(compressed) // 2)
        yield compressed[:midpoint]
        yield compressed[midpoint:]

    streaming_json = httpx.Response(
        200,
        content=json_chunks(),
        headers={"Content-Type": "application/json", "Content-Encoding": "gzip"},
    )
    assert list(streaming_json.iter_json()) == [1, 2, 3]


@pytest.mark.anyio
async def test_aiter_json_document_streaming() -> None:
    async def body() -> typing.AsyncIterator[bytes]:
        yield b"["
        yield b"1,"
        yield b"2]"

    response = httpx.Response(
        200, content=body(), headers={"Content-Type": "application/json"}
    )
    assert response.is_closed is False
    values = [item async for item in response.aiter_json()]
    assert values == [1, 2]
    assert response.is_closed is True
    assert response.is_stream_consumed is True
    with pytest.raises(httpx.StreamConsumed):
        [item async for item in response.aiter_json()]


@pytest.mark.anyio
async def test_aiter_json_in_memory_is_repeatable() -> None:
    response = httpx.Response(
        200,
        content=b'{"a":1}\n{"b":2}\n',
        headers={"Content-Type": "application/x-ndjson"},
    )
    first = [item async for item in response.aiter_json()]
    second = [item async for item in response.aiter_json()]
    assert first == second == [{"a": 1}, {"b": 2}]
    assert response.content == b'{"a":1}\n{"b":2}\n'


@pytest.mark.anyio
async def test_aiter_json_seq_and_errors() -> None:
    response = httpx.Response(
        200,
        content=b"\x1e1\n\x1e2\n",
        headers={"Content-Type": "application/json-seq"},
    )
    assert [item async for item in response.aiter_json()] == [1, 2]
    assert [item async for item in response.aiter_json()] == [1, 2]

    async def body() -> typing.AsyncIterator[bytes]:
        yield b"\x1e"
        yield b"\n"

    streaming = httpx.Response(
        200, content=body(), headers={"Content-Type": "application/json-seq"}
    )
    with pytest.raises(httpx.DecodingError):
        [item async for item in streaming.aiter_json()]
    assert streaming.is_closed is True
    with pytest.raises(httpx.StreamConsumed):
        [item async for item in streaming.aiter_json()]


@pytest.mark.anyio
async def test_aiter_json_invalid_content_type_does_not_consume() -> None:
    started = False

    async def body() -> typing.AsyncIterator[bytes]:
        nonlocal started
        started = True
        yield b"{}"

    response = httpx.Response(
        200, content=body(), headers={"Content-Type": "text/plain"}
    )
    with pytest.raises(httpx.DecodingError):
        [item async for item in response.aiter_json()]
    assert started is False
    assert response.is_stream_consumed is False
    assert response.is_closed is False


@pytest.mark.anyio
async def test_aiter_json_encoding_and_suffix() -> None:
    payload = json.dumps([{"n": 1}, "é"]).encode("utf-16")

    async def body() -> typing.AsyncIterator[bytes]:
        for index in range(0, len(payload), 3):
            yield payload[index : index + 3]

    response = httpx.Response(
        200,
        content=body(),
        headers={"Content-Type": "application/problem+json"},
    )
    assert [item async for item in response.aiter_json()] == [{"n": 1}, "é"]

import gzip
import json
import typing

import pytest

import httpx


def _response(
    content: bytes | typing.Iterable[bytes] | typing.AsyncIterable[bytes],
    content_type: str | None,
    **kwargs: typing.Any,
) -> httpx.Response:
    headers = kwargs.pop("headers", {})
    if content_type is not None:
        headers["Content-Type"] = content_type
    return httpx.Response(200, content=content, headers=headers, **kwargs)


def _values(
    content: bytes | typing.Iterable[bytes],
    content_type: str | None = "application/json",
) -> list[typing.Any]:
    return list(_response(content, content_type).iter_json())


def test_json_document_values() -> None:
    assert _values(b'{"a": 1}') == [{"a": 1}]
    assert _values(b'[1, {"a": 2}, [3]]') == [1, {"a": 2}, [3]]
    assert _values(b"[]") == []
    assert _values(b"{}") == [{}]
    assert _values(b"null") == [None]
    assert _values(b"true") == [True]
    assert _values(b"false") == [False]
    assert _values(b"0") == [0]
    assert _values(b'""') == [""]
    assert _values(b'"hello"') == ["hello"]
    assert _values(b"  \n\t[ 1 , 2 ]  \r\n") == [1, 2]


def test_json_document_leading_bom_and_whitespace() -> None:
    assert _values(b'\xef\xbb\xbf{"a": 1}') == [{"a": 1}]
    assert _values(b' \xef\xbb\xbf{"a": 1}') == [{"a": 1}]
    assert _values(b"\xef\xbb\xbf  \n[]") == []
    assert _values(
        b'\xef\xbb\xbf{"a": 1}',
        "application/json; charset=utf-8",
    ) == [{"a": 1}]


@pytest.mark.parametrize(
    "content",
    [b"", b"   ", b"\n\t\r", b"\xef\xbb\xbf", b"\xef\xbb\xbf   "],
)
def test_json_document_empty_is_error(content: bytes) -> None:
    with pytest.raises(httpx.DecodingError):
        _values(content)


@pytest.mark.parametrize(
    "content",
    [
        b"[1, 2] 3",
        b"1 2",
        b'{"a": 1}{"b": 2}',
        b"[1,]",
        b"[1 2]",
        b"[",
        b"[1",
        b'["a"',
        b'"unterminated',
    ],
)
def test_json_document_invalid(content: bytes) -> None:
    with pytest.raises(httpx.DecodingError):
        _values(content)


def test_json_suffix_and_rejected_media_types() -> None:
    assert _values(b"[1]", "application/vnd.api+json") == [1]
    assert _values(b'{"a": 1}', "Application/Problem+JSON; charset=utf-8") == [{"a": 1}]
    assert _values(b"[1]", 'application/json; foo=bar; charset="utf-8"') == [1]

    for content_type in [
        None,
        "",
        "text/plain",
        "text/json",
        "image/svg+json",
        "Image/SVG+JSON",
        "application/+json",
        "application/xml",
        "application/jsonl",
        "application/x-json-seq",
    ]:
        with pytest.raises(httpx.DecodingError):
            _values(b"[1]", content_type)


def test_invalid_charset() -> None:
    for content_type in [
        "application/json; charset=not-a-codec",
        "application/json; charset=",
        "application/json-seq; charset=rot_13",
    ]:
        with pytest.raises(httpx.DecodingError):
            _values(b"[1]", content_type)


def test_charset_decodes_before_json_parsing() -> None:
    body = '{"a":"é"}'.encode("latin-1")
    assert _values(body, "application/json; charset=latin-1") == [{"a": "é"}]
    with pytest.raises(httpx.DecodingError):
        _values(body, "application/json")
    with pytest.raises(httpx.DecodingError):
        _values(b'{"a":"\xff"}', "application/json; charset=utf-8")


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
def test_json_encoding_detection(encoding: str) -> None:
    payload = ["hello", {"n": 1}, None]
    assert _values(json.dumps(payload).encode(encoding)) == payload
    assert (
        _values(
            json.dumps(payload).encode(encoding),
            f"application/json; charset={encoding}",
        )
        == payload
    )


def test_explicit_utf_bom_prefixes() -> None:
    import codecs

    text = json.dumps({"a": 1})
    assert _values(codecs.BOM_UTF16_BE + text.encode("utf-16-be")) == [{"a": 1}]
    assert _values(codecs.BOM_UTF16_LE + text.encode("utf-16-le")) == [{"a": 1}]
    assert _values(codecs.BOM_UTF32_BE + text.encode("utf-32-be")) == [{"a": 1}]
    assert _values(codecs.BOM_UTF32_LE + text.encode("utf-32-le")) == [{"a": 1}]
    assert _values("1".encode("utf-16-be")) == [1]
    assert _values("1".encode("utf-16-le")) == [1]


def test_ndjson_lines() -> None:
    body = b'{"a": 1}\n\n  \r{"a": 2}\r\n{"a": 3}\r[1, 2]\n'
    assert _values(body, "application/ndjson") == [
        {"a": 1},
        {"a": 2},
        {"a": 3},
        [1, 2],
    ]
    assert _values(body, "application/x-ndjson; charset=utf-8") == [
        {"a": 1},
        {"a": 2},
        {"a": 3},
        [1, 2],
    ]
    assert _values(b'{"a": 1}', "Application/X-NDJSON") == [{"a": 1}]
    assert _values(b"", "application/ndjson") == []
    assert _values(b"\n\r\n  \r", "application/ndjson") == []
    assert _values(b'  {"a": 1}  \r\n\t[1]\t\n', "application/ndjson") == [
        {"a": 1},
        [1],
    ]


def test_ndjson_bom_rules() -> None:
    assert _values(
        b'\xef\xbb\xbf{"a": 1}\n{"b": 2}\n',
        "application/ndjson; charset=utf-8",
    ) == [{"a": 1}, {"b": 2}]
    assert _values(b'\n\xef\xbb\xbf{"a": 1}\n', "application/ndjson") == [{"a": 1}]
    assert _values(b'\xef\xbb\xbf{"a": 1}\n{"b": 2}\n', "application/ndjson") == [
        {"a": 1},
        {"b": 2},
    ]
    assert _values(b'\xef\xbb\xbf  {"a": 1}\n', "application/ndjson") == [{"a": 1}]
    assert _values(b"\xef\xbb\xbf", "application/ndjson") == []
    assert _values(b"\xef\xbb\xbf   ", "application/ndjson") == []
    assert _values_from_chunks(
        [b"\xef\xbb\xbf   ", b'{"a": 1}\n'],
        "application/ndjson",
    ) == [{"a": 1}]
    with pytest.raises(httpx.DecodingError):
        _values(b'\xef\xbb\xbf\n{"a": 1}\n', "application/ndjson; charset=utf-8")
    with pytest.raises(httpx.DecodingError):
        _values(b'\xef\xbb\xbf\n{"a": 1}\n', "application/ndjson")
    with pytest.raises(httpx.DecodingError):
        _values(b'\xef\xbb\xbf  \n{"a": 1}\n', "application/ndjson")
    with pytest.raises(httpx.DecodingError):
        _values_from_chunks(
            [b"\xef\xbb\xbf   ", b'\n{"a": 1}\n'],
            "application/ndjson",
        )
    with pytest.raises(httpx.DecodingError):
        _values(b'{"a": 1}\n\xef\xbb\xbf{"b": 2}\n', "application/ndjson")


def test_ndjson_rejects_non_json_whitespace_line_breaks_and_extra_values() -> None:
    with pytest.raises(httpx.DecodingError):
        _values(b"1\xe2\x80\xa82\n", "application/ndjson")
    with pytest.raises(httpx.DecodingError):
        _values(b"1 2\n", "application/ndjson")
    with pytest.raises(httpx.DecodingError):
        _values(b"{}\n[\n", "application/ndjson")


def test_json_seq_records() -> None:
    rs = b"\x1e"
    body = rs + b'{"a": 1}\n' + rs + b'{"b": 2}\n'
    assert _values(body, "application/json-seq") == [{"a": 1}, {"b": 2}]
    assert _values(body, "Application/JSON-SEQ; charset=utf-8") == [
        {"a": 1},
        {"b": 2},
    ]
    assert _values(b"   \n\t", "application/json-seq") == []
    assert _values(b"", "application/json-seq") == []
    assert _values(b"  " + rs + b"[1, 2]\n", "application/json-seq") == [[1, 2]]
    assert _values(rs + b"  [1, 2]  \n", "application/json-seq") == [[1, 2]]
    assert _values(rs + rs + b'{"a": 1}\n', "application/json-seq") == [{"a": 1}]
    assert _values(rs + b"   \n" + rs + b"1\n", "application/json-seq") == [1]
    assert _values(rs + b'{"a": 1}\n\n', "application/json-seq") == [{"a": 1}]
    assert _values(rs + b'{"a": 1}\r\n', "application/json-seq") == [{"a": 1}]
    assert _values(rs + b'"\\u001e"\n', "application/json-seq") == ["\x1e"]
    assert _values(b"\xef\xbb\xbf" + rs + b"1\n", "application/json-seq") == [1]


@pytest.mark.parametrize(
    "content",
    [
        b'{"a": 1}',
        b"\x1e",
        b"\x1e\n",
        b"\x1e   \n",
        b"\x1e\x1e",
        b'\x1e{"a": 1}\n\x1e',
        b'\x1e{"a": 1}\n\x1e\n',
        b'\x1e{"a": 1}\n\x1e   \n',
        b"\x1e1 2\n",
        b"\x1e1x\n",
        b'\x1e"\x1e"\n',
        b"   x",
    ],
)
def test_json_seq_errors(content: bytes) -> None:
    with pytest.raises(httpx.DecodingError):
        _values(content, "application/json-seq")


def test_in_memory_json_iteration_is_repeatable() -> None:
    response = httpx.Response(200, json=[1, {"a": 2}])
    assert list(response.iter_json()) == [1, {"a": 2}]
    assert list(response.iter_json()) == [1, {"a": 2}]
    assert response.is_closed


def test_streaming_json_consumes_and_closes() -> None:
    pulled: list[int] = []

    def body() -> typing.Iterator[bytes]:
        pulled.append(1)
        yield b"[1, "
        pulled.append(2)
        yield b"2, "
        pulled.append(3)
        yield b"3]"

    response = _response(body(), "application/json")
    assert not response.is_closed
    iterator = response.iter_json()
    assert next(iterator) == 1
    assert pulled == [1]
    assert list(iterator) == [2, 3]
    assert pulled == [1, 2, 3]
    assert response.is_closed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_json())


def _values_from_chunks(
    chunks: typing.Iterable[bytes],
    content_type: str,
) -> list[typing.Any]:
    return list(_response(chunks, content_type).iter_json())


def test_streaming_chunks_cover_partial_tokens() -> None:
    payload = b'[{"a": "hello\\n"}, 12, true]'
    assert _values_from_chunks([b"1", b"2"], "application/json") == [12]
    assert _values_from_chunks([b"tr", b"ue"], "application/json") == [True]
    assert _values_from_chunks([b'"hello\\', b'n"'], "application/json") == ["hello\n"]
    assert _values_from_chunks(
        [payload[index : index + 1] for index in range(len(payload))],
        "application/json",
    ) == [{"a": "hello\n"}, 12, True]

    utf16 = json.dumps(["é", 2]).encode("utf-16-be")
    assert _values_from_chunks(
        [utf16[index : index + 1] for index in range(len(utf16))],
        "application/json",
    ) == ["é", 2]

    text = '{"a":"é"}'.encode()
    assert _values_from_chunks(
        [text[:-1], text[-1:]],
        "application/json; charset=utf-8",
    ) == [{"a": "é"}]


def test_ndjson_and_json_seq_are_chunk_safe() -> None:
    def ndjson() -> typing.Iterator[bytes]:
        yield b'{"a": 1}\r'
        yield b'\n\n{"a": 2}\r\n'
        yield b'{"a": 3}'

    assert list(_response(ndjson(), "application/ndjson").iter_json()) == [
        {"a": 1},
        {"a": 2},
        {"a": 3},
    ]

    def seq() -> typing.Iterator[bytes]:
        yield b"  \x1e"
        yield b'{"a": 1}\n\x1e'
        yield b'{"b": 2}\n'

    assert list(_response(seq(), "application/json-seq").iter_json()) == [
        {"a": 1},
        {"b": 2},
    ]


def test_wrong_content_type_does_not_consume_stream() -> None:
    def body() -> typing.Iterator[bytes]:
        yield b"[1]"

    response = _response(body(), "text/plain")
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())
    assert not response.is_stream_consumed
    assert not response.is_closed
    assert response.read() == b"[1]"


def test_streaming_parse_error_closes_response() -> None:
    def body() -> typing.Iterator[bytes]:
        yield b"[1] trailing"
        yield b" more"

    response = _response(
        body(),
        "application/json",
        request=httpx.Request("GET", "https://example.org/json"),
    )
    with pytest.raises(httpx.DecodingError) as exc_info:
        list(response.iter_json())
    assert exc_info.value.request.url == "https://example.org/json"
    assert response.is_closed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_json())


def test_gzip_encoded_json_stream() -> None:
    compressed = gzip.compress(b"[1, 2]")
    midpoint = max(1, len(compressed) // 2)

    def body() -> typing.Iterator[bytes]:
        yield compressed[:midpoint]
        yield compressed[midpoint:]

    response = httpx.Response(
        200,
        content=body(),
        headers={
            "Content-Type": "application/json",
            "Content-Encoding": "gzip",
        },
    )
    assert list(response.iter_json()) == [1, 2]


@pytest.mark.anyio
async def test_aiter_json_repeatable_and_streaming() -> None:
    response = httpx.Response(200, json={"a": [1]})
    assert [value async for value in response.aiter_json()] == [{"a": [1]}]
    assert [value async for value in response.aiter_json()] == [{"a": [1]}]

    async def body() -> typing.AsyncIterator[bytes]:
        yield b'{"a": 1}\n'
        yield b'{"a": 2}\n'

    streaming = _response(body(), "application/x-ndjson")
    assert [value async for value in streaming.aiter_json()] == [{"a": 1}, {"a": 2}]
    assert streaming.is_closed
    with pytest.raises(httpx.StreamConsumed):
        _ = [value async for value in streaming.aiter_json()]


@pytest.mark.anyio
async def test_aiter_json_finish_parses_or_errors() -> None:
    async def numbers() -> typing.AsyncIterator[bytes]:
        yield b"1"
        yield b"2"

    response = _response(numbers(), "application/json")
    assert [value async for value in response.aiter_json()] == [12]

    incomplete = _response(b"[", "application/json")
    with pytest.raises(httpx.DecodingError):
        _ = [value async for value in incomplete.aiter_json()]


@pytest.mark.anyio
async def test_aiter_json_streaming_error_closes() -> None:
    async def body() -> typing.AsyncIterator[bytes]:
        yield b"[1] x"
        yield b"y"

    response = _response(body(), "application/json")
    with pytest.raises(httpx.DecodingError):
        _ = [value async for value in response.aiter_json()]
    assert response.is_closed

    async def plain() -> typing.AsyncIterator[bytes]:
        yield b"[1]"

    untouched = _response(plain(), "image/svg+json")
    with pytest.raises(httpx.DecodingError):
        _ = [value async for value in untouched.aiter_json()]
    assert not untouched.is_stream_consumed
    assert await untouched.aread() == b"[1]"

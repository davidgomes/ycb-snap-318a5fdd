from __future__ import annotations

import gzip
import json
import math
from typing import Any, AsyncIterator, Iterator

import pytest

import httpx


def _response(
    content: bytes | Iterator[bytes] | AsyncIterator[bytes],
    content_type: str | None = "application/json",
    headers: dict[str, str] | None = None,
) -> httpx.Response:
    merged: dict[str, str] = {}
    if content_type is not None:
        merged["Content-Type"] = content_type
    if headers:
        merged.update(headers)
    return httpx.Response(200, content=content, headers=merged)


def _chunks(parts: list[bytes]) -> Iterator[bytes]:
    yield from parts


async def _achunks(parts: list[bytes]) -> AsyncIterator[bytes]:
    for part in parts:
        yield part


def _split(payload: bytes, size: int) -> list[bytes]:
    return [payload[index : index + size] for index in range(0, len(payload), size)]


def test_json_array_yields_elements() -> None:
    response = _response(b' [1, {"a": 2}, [3], "x", true, false, null] ')
    assert list(response.iter_json()) == [1, {"a": 2}, [3], "x", True, False, None]


def test_json_single_value_and_repeatable() -> None:
    response = _response(b'  {"a": 1}  ')
    assert list(response.iter_json()) == [{"a": 1}]
    assert list(response.iter_json()) == [{"a": 1}]
    assert response.read() == b'  {"a": 1}  '


def test_json_empty_array_is_not_an_error() -> None:
    response = _response(b" [ ] ")
    assert list(response.iter_json()) == []
    assert list(response.iter_json()) == []


@pytest.mark.parametrize(
    "payload",
    [b"", b"   ", b"\n\t\r", "\ufeff".encode(), " \ufeff  ".encode()],
)
def test_json_empty_payload_errors(payload: bytes) -> None:
    response = _response(payload)
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())


def test_json_utf8_bom_and_leading_whitespace() -> None:
    payload = b"\xef\xbb\xbf  [1, 2]"
    assert list(_response(payload).iter_json()) == [1, 2]
    payload = '  \ufeff{"a": 1}'.encode()
    assert list(_response(payload).iter_json()) == [{"a": 1}]
    payload = '\ufeff\n{"a": 1}'.encode()
    assert list(_response(payload).iter_json()) == [{"a": 1}]


def test_json_trailing_data_errors() -> None:
    for payload in (
        b"[1, 2] x",
        b'{"a": 1}{"b": 2}',
        b"1 2",
        b"truee",
        b"01",
        b"[1,]",
        b"[1, 2,]",
    ):
        with pytest.raises(httpx.DecodingError):
            list(_response(payload).iter_json())


def test_json_array_does_not_yield_element_with_illegal_tail() -> None:
    values: list[Any] = []
    with pytest.raises(httpx.DecodingError):
        for value in _response(b"[1, 2x]").iter_json():
            values.append(value)
    assert values == [1]


def test_plus_json_suffix_only_for_application() -> None:
    payload = b"[1, 2]"
    assert list(_response(payload, "application/vnd.api+json").iter_json()) == [1, 2]
    assert list(
        _response(payload, "Application/Problem+JSON; charset=utf-8").iter_json()
    ) == [
        1,
        2,
    ]
    with pytest.raises(httpx.DecodingError):
        list(_response(payload, "image/svg+json").iter_json())
    with pytest.raises(httpx.DecodingError):
        list(_response(payload, "text/html+json").iter_json())


@pytest.mark.parametrize(
    "content_type",
    [
        None,
        "text/plain",
        "application/jsonl",
        "application/x-jsonlines",
        "text/json",
        "application/octet-stream",
    ],
)
def test_rejected_media_types(content_type: str | None) -> None:
    response = _response(b"[1]", content_type=content_type)
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())
    # Rejecting the media type must not consume an unread stream.
    assert response.content == b"[1]"


def test_rejected_media_type_does_not_consume_stream() -> None:
    def body() -> Iterator[bytes]:
        yield b"[1, 2]"

    response = _response(body(), content_type="text/plain")
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())
    assert not response.is_stream_consumed
    assert response.read() == b"[1, 2]"


@pytest.mark.parametrize(
    "content_type",
    [
        "application/json; charset=utf-8",
        "application/json;charset=UTF-8",
        'application/json; charset="utf-8"',
        "application/json; foo=bar",
        "application/json ; charset = utf-8",
        "APPLICATION/JSON",
    ],
)
def test_media_type_parameters_and_case(content_type: str) -> None:
    assert list(_response(b'{"a": 1}', content_type).iter_json()) == [{"a": 1}]


def test_invalid_charset() -> None:
    response = _response(b"{}", "application/json; charset=not-a-charset")
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())
    response = _response(b"{}", "application/ndjson; charset=")
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())


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
    payload = json.dumps([{"greeting": "hello"}, 2]).encode(encoding)
    assert list(_response(payload).iter_json()) == [{"greeting": "hello"}, 2]
    charset_payload = json.dumps({"greeting": "hello"}).encode(encoding)
    typed = _response(charset_payload, f"application/json; charset={encoding}")
    assert list(typed.iter_json()) == [{"greeting": "hello"}]


def test_json_bom_with_explicit_utf8_charset() -> None:
    payload = b'\xef\xbb\xbf{"a": 1}'
    response = _response(payload, "application/json; charset=utf-8")
    assert list(response.iter_json()) == [{"a": 1}]


def test_mismatched_charset_errors() -> None:
    payload = json.dumps({"a": 1}).encode("utf-16")
    response = _response(payload, "application/json; charset=utf-8")
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())


def test_ndjson_lines() -> None:
    payload = b'\n{"a": 1}\r\n\n{"b": 2}\r{"c": 3}\n  \n'
    assert list(_response(payload, "application/ndjson").iter_json()) == [
        {"a": 1},
        {"b": 2},
        {"c": 3},
    ]
    assert list(
        _response(payload, "application/x-ndjson; charset=utf-8").iter_json()
    ) == [
        {"a": 1},
        {"b": 2},
        {"c": 3},
    ]


def test_ndjson_empty_and_whitespace() -> None:
    assert list(_response(b"", "application/ndjson").iter_json()) == []
    assert list(_response(b"\n\r\n  \t\r", "application/x-ndjson").iter_json()) == []


def test_ndjson_exact_text_and_bom() -> None:
    payload = b'\xef\xbb\xbf{"a": 1}\n{"b": 2}\n'
    assert list(_response(payload, "application/ndjson").iter_json()) == [
        {"a": 1},
        {"b": 2},
    ]
    payload = '\n\n\ufeff{"a": 1}\n'.encode()
    assert list(
        _response(payload, "application/ndjson; charset=utf-8").iter_json()
    ) == [{"a": 1}]
    payload = '{"a": 1}\n\ufeff{"b": 2}\n'.encode()
    with pytest.raises(httpx.DecodingError):
        list(_response(payload, "application/ndjson").iter_json())
    payload = b'{"a": 1}{"b": 2}\n'
    with pytest.raises(httpx.DecodingError):
        list(_response(payload, "application/ndjson").iter_json())
    payload = '  \ufeff{"a": 1}\n'.encode()
    with pytest.raises(httpx.DecodingError):
        list(_response(payload, "application/ndjson; charset=utf-8").iter_json())


def test_ndjson_array_line_is_one_value() -> None:
    payload = b"[1, 2]\n3\n"
    assert list(_response(payload, "application/ndjson").iter_json()) == [[1, 2], 3]


def test_json_seq_records() -> None:
    payload = b'  \n\x1e{"a": 1}\n\x1e{"b": 2}\n'
    assert list(_response(payload, "application/json-seq").iter_json()) == [
        {"a": 1},
        {"b": 2},
    ]


def test_json_seq_empty_payload_yields_nothing() -> None:
    for payload in (b"", b"   ", b"\n\t\r "):
        assert list(_response(payload, "application/json-seq").iter_json()) == []


@pytest.mark.parametrize(
    "payload",
    [
        b"\x1e",
        b"\x1e\n",
        b"\x1e   \n",
        b"\x1e\n\n",
        b'\x1e{"a": 1}\x1e',
        b'\x1e{"a": 1}\x1e   ',
        b'\x1e{"a": 1}\n\x1e\n',
        b'\x1e{"a": 1}\x1e\x1e',
        b'{"a": 1}',
        b'x\x1e{"a": 1}\n',
        b'\x1e{"a": 1}{"b": 2}\n',
        b"\x1e[1, 2] trailing",
    ],
)
def test_json_seq_errors(payload: bytes) -> None:
    with pytest.raises(httpx.DecodingError):
        list(_response(payload, "application/json-seq").iter_json())


def test_json_seq_ignores_empty_records_between_separators() -> None:
    payload = b'\x1e\x1e\n\x1e   \x1e{"a": 1}\n'
    assert list(_response(payload, "application/json-seq").iter_json()) == [{"a": 1}]


def test_json_seq_array_is_one_value() -> None:
    payload = b"\x1e[1, 2, 3]\n"
    assert list(_response(payload, "application/json-seq").iter_json()) == [[1, 2, 3]]


def test_json_seq_value_without_trailing_lf() -> None:
    payload = b'\x1e{"a": 1}'
    assert list(_response(payload, "application/json-seq").iter_json()) == [{"a": 1}]


def test_streaming_consumes_and_closes() -> None:
    def body() -> Iterator[bytes]:
        yield b"[1, "
        yield b"2, 3]"

    response = _response(body())
    assert not response.is_closed
    assert list(response.iter_json()) == [1, 2, 3]
    assert response.is_closed
    assert response.is_stream_consumed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_json())


def test_streaming_partial_iteration_closes() -> None:
    def body() -> Iterator[bytes]:
        yield b"[1, 2, 3]"

    response = _response(body())
    iterator = response.iter_json()
    assert next(iterator) == 1
    iterator.close()
    assert response.is_closed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_json())


def test_incremental_array_does_not_buffer_the_next_chunk() -> None:
    class FlagStream(httpx.SyncByteStream):
        def __init__(self) -> None:
            self.reads = 0

        def __iter__(self) -> Iterator[bytes]:
            self.reads += 1
            yield b"[1, "
            self.reads += 1
            yield b"2]"

    stream = FlagStream()
    response = httpx.Response(
        200,
        stream=stream,
        headers={"Content-Type": "application/json"},
    )
    iterator = response.iter_json()
    assert next(iterator) == 1
    assert stream.reads == 1
    assert next(iterator) == 2
    assert stream.reads == 2
    with pytest.raises(StopIteration):
        next(iterator)


def test_number_split_across_chunks() -> None:
    response = _response(_chunks([b"12", b"3"]))
    assert list(response.iter_json()) == [123]
    response = _response(_chunks([b"1", b"e", b"2"]))
    assert list(response.iter_json()) == [100.0]
    response = _response(_chunks([b"[12", b"3, ", b"4]"]))
    assert list(response.iter_json()) == [123, 4]


def test_one_byte_chunks_for_each_format() -> None:
    document = '[1, {"ok": true}, "hi"]'.encode()
    assert list(_response(_chunks(_split(document, 1))).iter_json()) == [
        1,
        {"ok": True},
        "hi",
    ]
    ndjson = b'{"a": 1}\r\n{"b": 2}\n'
    assert list(
        _response(_chunks(_split(ndjson, 1)), "application/ndjson").iter_json()
    ) == [{"a": 1}, {"b": 2}]
    sequence = b'\x1e{"a": 1}\n\x1e{"b": 2}'
    assert list(
        _response(_chunks(_split(sequence, 1)), "application/json-seq").iter_json()
    ) == [{"a": 1}, {"b": 2}]


def test_utf16_split_across_odd_chunks() -> None:
    payload = json.dumps([1, {"a": "héllo"}]).encode("utf-16")
    response = _response(_chunks(_split(payload, 3)))
    assert list(response.iter_json()) == [1, {"a": "héllo"}]


def test_nonstandard_constants_match_json_loads() -> None:
    response = _response(b"[NaN, Infinity, -Infinity]")
    values = list(response.iter_json())
    assert math.isnan(values[0])
    assert values[1] == float("inf")
    assert values[2] == float("-inf")


def test_gzip_content_encoding_is_decoded() -> None:
    raw = b'{"a": 1}\n{"b": 2}\n'
    compressed = gzip.compress(raw)

    def body() -> Iterator[bytes]:
        midpoint = len(compressed) // 2
        yield compressed[:midpoint]
        yield compressed[midpoint:]

    response = _response(
        body(),
        "application/ndjson",
        headers={"Content-Encoding": "gzip"},
    )
    assert list(response.iter_json()) == [{"a": 1}, {"b": 2}]


def test_response_json_constructor() -> None:
    response = httpx.Response(200, json=[1, 2, {"a": 3}])
    assert list(response.iter_json()) == [1, 2, {"a": 3}]
    response = httpx.Response(200, json={"a": 1})
    assert list(response.iter_json()) == [{"a": 1}]


@pytest.mark.anyio
async def test_aiter_json_array_and_repeatable() -> None:
    response = _response(b"[1, 2, 3]")
    assert [value async for value in response.aiter_json()] == [1, 2, 3]
    assert [value async for value in response.aiter_json()] == [1, 2, 3]


@pytest.mark.anyio
async def test_aiter_json_streaming_consumes() -> None:
    response = _response(
        _achunks([b'{"a": ', b"1}\n", b'{"b": 2}\n']), "application/ndjson"
    )
    assert [value async for value in response.aiter_json()] == [{"a": 1}, {"b": 2}]
    assert response.is_closed
    with pytest.raises(httpx.StreamConsumed):
        _ = [value async for value in response.aiter_json()]


@pytest.mark.anyio
async def test_aiter_json_content_type_and_seq() -> None:
    response = _response(b"[]", "image/svg+json")
    with pytest.raises(httpx.DecodingError):
        _ = [value async for value in response.aiter_json()]
    response = _response(b"\x1e1\n\x1e2\n", "application/json-seq")
    assert [value async for value in response.aiter_json()] == [1, 2]
    response = _response(b"   ", "application/json")
    with pytest.raises(httpx.DecodingError):
        _ = [value async for value in response.aiter_json()]


def test_ndjson_crlf_split_across_chunks() -> None:
    response = _response(
        _chunks([b'{"a": 1}\r', b'\n{"b": 2}\n']),
        "application/ndjson",
    )
    assert list(response.iter_json()) == [{"a": 1}, {"b": 2}]
    response = _response(
        _chunks([b'{"a": 1}\r', b'{"b": 2}']),
        "application/ndjson",
    )
    assert list(response.iter_json()) == [{"a": 1}, {"b": 2}]


def test_json_seq_record_split_across_chunks() -> None:
    response = _response(
        _chunks([b'\x1e{"a":', b" 1}\n\x1e", b'{"b": 2}']),
        "application/json-seq",
    )
    assert list(response.iter_json()) == [{"a": 1}, {"b": 2}]


def test_scalars_and_empty_containers() -> None:
    assert list(_response(b"null").iter_json()) == [None]
    assert list(_response(b"false").iter_json()) == [False]
    assert list(_response(b"0").iter_json()) == [0]
    assert list(_response(b'"hi"').iter_json()) == ["hi"]
    assert list(_response(b"{}").iter_json()) == [{}]
    assert list(_response(b"[null, false, 0, {}, []]").iter_json()) == [
        None,
        False,
        0,
        {},
        [],
    ]


def test_cross_chunk_trailing_data_is_not_yielded() -> None:
    values: list[Any] = []
    response = _response(_chunks([b'{"a": 1}', b" x"]))
    with pytest.raises(httpx.DecodingError):
        for value in response.iter_json():
            values.append(value)
    assert values == []

    values = []
    response = _response(_chunks([b"[1, 2", b"x]"]))
    with pytest.raises(httpx.DecodingError):
        for value in response.iter_json():
            values.append(value)
    assert values == [1]

    values = []
    response = _response(_chunks([b"[1 ", b"2]"]))
    with pytest.raises(httpx.DecodingError):
        for value in response.iter_json():
            values.append(value)
    assert values == []

    values = []
    response = _response(
        _chunks([b'\x1e{"a": 1}\n', b"x"]),
        "application/json-seq",
    )
    with pytest.raises(httpx.DecodingError):
        for value in response.iter_json():
            values.append(value)
    assert values == []


def test_chunked_top_level_value_and_literals() -> None:
    response = _response(_chunks([b'{"a":', b" 1}"]))
    assert list(response.iter_json()) == [{"a": 1}]
    payload = "[true, false, null, NaN, Infinity, -Infinity]".encode()
    assert list(_response(_chunks(_split(payload, 1))).iter_json())[:3] == [
        True,
        False,
        None,
    ]


def test_in_memory_iteration_is_repeatable_after_partial_or_error() -> None:
    response = _response(b"[1, 2, 3]")
    iterator = response.iter_json()
    assert next(iterator) == 1
    iterator.close()
    assert list(response.iter_json()) == [1, 2, 3]

    response = _response(b'{"a": 1} x')
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())
    assert response.content == b'{"a": 1} x'


def test_streaming_decode_error_closes_response() -> None:
    def body() -> Iterator[bytes]:
        yield b"[1, 2x]"

    response = _response(body())
    values: list[Any] = []
    with pytest.raises(httpx.DecodingError):
        for value in response.iter_json():
            values.append(value)
    assert values == [1]
    assert response.is_closed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_json())


def test_explicit_charset_and_json_seq_case() -> None:
    payload = '{"é": 1}\n'.encode("latin-1")
    response = _response(payload, "application/ndjson; charset=iso-8859-1")
    assert list(response.iter_json()) == [{"é": 1}]
    payload = b'\x1e{"a": 1}\n'
    assert list(_response(payload, "Application/JSON-SEQ").iter_json()) == [{"a": 1}]


def test_request_is_attached_to_decoding_error() -> None:
    request = httpx.Request("GET", "https://example.org")
    response = _response(b"[1, 2] trailing", headers={})
    response.request = request
    with pytest.raises(httpx.DecodingError) as exc_info:
        list(response.iter_json())
    assert exc_info.value.request is request

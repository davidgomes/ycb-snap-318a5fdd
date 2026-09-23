import json
import typing

import pytest

import httpx


def _response(
    content: bytes | typing.Iterator[bytes] | typing.AsyncIterator[bytes],
    content_type: str,
) -> httpx.Response:
    return httpx.Response(
        200,
        content=content,
        headers={"Content-Type": content_type},
        request=httpx.Request("GET", "https://example.org"),
    )


def _chunks(payload: bytes, size: int = 1) -> typing.Iterator[bytes]:
    for index in range(0, len(payload), size):
        yield payload[index : index + size]


async def _achunks(payload: bytes, size: int = 1) -> typing.AsyncIterator[bytes]:
    for index in range(0, len(payload), size):
        yield payload[index : index + size]


def test_iter_json_array_and_repeatable_for_in_memory_body():
    response = _response(b' [1, {"a": 2}, [3]] ', "application/json")

    assert list(response.iter_json()) == [1, {"a": 2}, [3]]
    assert list(response.iter_json()) == [1, {"a": 2}, [3]]


def test_iter_json_single_value():
    response = _response(b'  {"hello": "world"}  ', "application/vnd.api+json")
    assert list(response.iter_json()) == [{"hello": "world"}]


def test_iter_json_streaming_consumes_and_closes():
    response = _response(_chunks(b"[10,20,30]", size=3), "application/json")

    assert list(response.iter_json()) == [10, 20, 30]
    assert response.is_closed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_json())


def test_iter_json_number_split_across_chunks():
    response = _response(_chunks(b"[12,3.5,1e2]"), "application/json")
    assert list(response.iter_json()) == [12, 3.5, 100]


def test_iter_json_rejects_empty_trailing_and_bad_types():
    with pytest.raises(httpx.DecodingError):
        list(_response(b"   ", "application/json").iter_json())
    with pytest.raises(httpx.DecodingError):
        list(_response(b"", "application/json").iter_json())
    with pytest.raises(httpx.DecodingError):
        list(_response(b"[1,]", "application/json").iter_json())
    with pytest.raises(httpx.DecodingError):
        list(_response(b"1 2", "application/json").iter_json())
    with pytest.raises(httpx.DecodingError):
        list(_response(b"{}", "image/svg+json").iter_json())
    with pytest.raises(httpx.DecodingError):
        list(_response(b"{}", "text/plain").iter_json())
    with pytest.raises(httpx.DecodingError):
        list(_response(b"{}", "application/json; charset=not-a-codec").iter_json())


def test_bad_content_type_does_not_consume_stream():
    response = _response(_chunks(b'{"a":1}'), "text/plain")
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())
    assert response.read() == b'{"a":1}'


def test_charset_and_bom_detection():
    payload = b"\xef\xbb\xbf" + json.dumps({"ok": True}).encode("utf-8")
    assert list(_response(payload, "application/json").iter_json()) == [{"ok": True}]

    utf16 = json.dumps([1, 2]).encode("utf-16")
    assert list(_response(utf16, "application/json").iter_json()) == [1, 2]
    assert list(_response(utf16, "Application/JSON; Charset=UTF-16").iter_json()) == [
        1,
        2,
    ]


def test_ndjson_lines_and_bom():
    payload = "\n\ufeff{\"a\": 1}\r\n\n2 \r{\"b\": 3}\n".encode("utf-8")
    response = _response(_chunks(payload, size=4), "application/x-ndjson")
    assert list(response.iter_json()) == [{"a": 1}, 2, {"b": 3}]

    blank = _response(b"\n\n  \n", "application/ndjson; charset=utf-8")
    assert list(blank.iter_json()) == []

    with pytest.raises(httpx.DecodingError):
        list(_response(b"1 2\n", "application/ndjson").iter_json())
    with pytest.raises(httpx.DecodingError):
        bom_later = "\n\ufeff\n\ufeff{}".encode("utf-8")
        list(_response(bom_later, "application/ndjson").iter_json())


def test_json_seq_records():
    payload = b"  \x1e{\"a\":1}\n\x1e\n\x1e [2, 3] \n"
    assert list(_response(payload, "application/json-seq").iter_json()) == [
        {"a": 1},
        [2, 3],
    ]
    assert list(_response(b"   \n", "application/json-seq").iter_json()) == []
    assert list(_response(b"", "application/json-seq; charset=utf-8").iter_json()) == []

    with pytest.raises(httpx.DecodingError):
        list(_response(b"\x1e", "application/json-seq").iter_json())
    with pytest.raises(httpx.DecodingError):
        list(_response(b"\x1e\n", "application/json-seq").iter_json())
    with pytest.raises(httpx.DecodingError):
        list(_response(b"\x1e   \n", "application/json-seq").iter_json())
    with pytest.raises(httpx.DecodingError):
        list(_response(b"\x1e1\x1e", "application/json-seq").iter_json())
    with pytest.raises(httpx.DecodingError):
        list(_response(b"{\"a\":1}", "application/json-seq").iter_json())
    with pytest.raises(httpx.DecodingError):
        list(_response(b"\x1e1 2", "application/json-seq").iter_json())


@pytest.mark.anyio
async def test_aiter_json_streaming_and_repeatable():
    streamed = _response(_achunks(b'[{"n":1},2]', size=2), "application/json")
    assert [item async for item in streamed.aiter_json()] == [{"n": 1}, 2]
    assert streamed.is_closed
    with pytest.raises(httpx.StreamConsumed):
        async for _ in streamed.aiter_json():
            pass

    memory = _response(b"\x1e1\n\x1e2\n", "application/json-seq")
    assert [item async for item in memory.aiter_json()] == [1, 2]
    assert [item async for item in memory.aiter_json()] == [1, 2]

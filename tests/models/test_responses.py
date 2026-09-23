import gzip
import json
import pickle
import typing

import chardet
import pytest

import httpx


class StreamingBody:
    def __iter__(self):
        yield b"Hello, "
        yield b"world!"


def streaming_body() -> typing.Iterator[bytes]:
    yield b"Hello, "
    yield b"world!"


async def async_streaming_body() -> typing.AsyncIterator[bytes]:
    yield b"Hello, "
    yield b"world!"


def autodetect(content):
    return chardet.detect(content).get("encoding")


def test_response():
    response = httpx.Response(
        200,
        content=b"Hello, world!",
        request=httpx.Request("GET", "https://example.org"),
    )

    assert response.status_code == 200
    assert response.reason_phrase == "OK"
    assert response.text == "Hello, world!"
    assert response.request.method == "GET"
    assert response.request.url == "https://example.org"
    assert not response.is_error


def test_response_content():
    response = httpx.Response(200, content="Hello, world!")

    assert response.status_code == 200
    assert response.reason_phrase == "OK"
    assert response.text == "Hello, world!"
    assert response.headers == {"Content-Length": "13"}


def test_response_text():
    response = httpx.Response(200, text="Hello, world!")

    assert response.status_code == 200
    assert response.reason_phrase == "OK"
    assert response.text == "Hello, world!"
    assert response.headers == {
        "Content-Length": "13",
        "Content-Type": "text/plain; charset=utf-8",
    }


def test_response_html():
    response = httpx.Response(200, html="<html><body>Hello, world!</html></body>")

    assert response.status_code == 200
    assert response.reason_phrase == "OK"
    assert response.text == "<html><body>Hello, world!</html></body>"
    assert response.headers == {
        "Content-Length": "39",
        "Content-Type": "text/html; charset=utf-8",
    }


def test_response_json():
    response = httpx.Response(200, json={"hello": "world"})

    assert response.status_code == 200
    assert response.reason_phrase == "OK"
    assert str(response.json()) == "{'hello': 'world'}"
    assert response.headers == {
        "Content-Length": "17",
        "Content-Type": "application/json",
    }


def test_raise_for_status():
    request = httpx.Request("GET", "https://example.org")

    # 2xx status codes are not an error.
    response = httpx.Response(200, request=request)
    response.raise_for_status()

    # 1xx status codes are informational responses.
    response = httpx.Response(101, request=request)
    assert response.is_informational
    with pytest.raises(httpx.HTTPStatusError) as exc_info:
        response.raise_for_status()
    assert str(exc_info.value) == (
        "Informational response '101 Switching Protocols' for url 'https://example.org'\n"
        "For more information check: https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/101"
    )

    # 3xx status codes are redirections.
    headers = {"location": "https://other.org"}
    response = httpx.Response(303, headers=headers, request=request)
    assert response.is_redirect
    with pytest.raises(httpx.HTTPStatusError) as exc_info:
        response.raise_for_status()
    assert str(exc_info.value) == (
        "Redirect response '303 See Other' for url 'https://example.org'\n"
        "Redirect location: 'https://other.org'\n"
        "For more information check: https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/303"
    )

    # 4xx status codes are a client error.
    response = httpx.Response(403, request=request)
    assert response.is_client_error
    assert response.is_error
    with pytest.raises(httpx.HTTPStatusError) as exc_info:
        response.raise_for_status()
    assert str(exc_info.value) == (
        "Client error '403 Forbidden' for url 'https://example.org'\n"
        "For more information check: https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/403"
    )

    # 5xx status codes are a server error.
    response = httpx.Response(500, request=request)
    assert response.is_server_error
    assert response.is_error
    with pytest.raises(httpx.HTTPStatusError) as exc_info:
        response.raise_for_status()
    assert str(exc_info.value) == (
        "Server error '500 Internal Server Error' for url 'https://example.org'\n"
        "For more information check: https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/500"
    )

    # Calling .raise_for_status without setting a request instance is
    # not valid. Should raise a runtime error.
    response = httpx.Response(200)
    with pytest.raises(RuntimeError):
        response.raise_for_status()


def test_response_repr():
    response = httpx.Response(
        200,
        content=b"Hello, world!",
    )
    assert repr(response) == "<Response [200 OK]>"


def test_response_content_type_encoding():
    """
    Use the charset encoding in the Content-Type header if possible.
    """
    headers = {"Content-Type": "text-plain; charset=latin-1"}
    content = "Latin 1: ÿ".encode("latin-1")
    response = httpx.Response(
        200,
        content=content,
        headers=headers,
    )
    assert response.text == "Latin 1: ÿ"
    assert response.encoding == "latin-1"


def test_response_default_to_utf8_encoding():
    """
    Default to utf-8 encoding if there is no Content-Type header.
    """
    content = "おはようございます。".encode("utf-8")
    response = httpx.Response(
        200,
        content=content,
    )
    assert response.text == "おはようございます。"
    assert response.encoding == "utf-8"


def test_response_fallback_to_utf8_encoding():
    """
    Fallback to utf-8 if we get an invalid charset in the Content-Type header.
    """
    headers = {"Content-Type": "text-plain; charset=invalid-codec-name"}
    content = "おはようございます。".encode("utf-8")
    response = httpx.Response(
        200,
        content=content,
        headers=headers,
    )
    assert response.text == "おはようございます。"
    assert response.encoding == "utf-8"


def test_response_no_charset_with_ascii_content():
    """
    A response with ascii encoded content should decode correctly,
    even with no charset specified.
    """
    content = b"Hello, world!"
    headers = {"Content-Type": "text/plain"}
    response = httpx.Response(
        200,
        content=content,
        headers=headers,
    )
    assert response.status_code == 200
    assert response.encoding == "utf-8"
    assert response.text == "Hello, world!"


def test_response_no_charset_with_utf8_content():
    """
    A response with UTF-8 encoded content should decode correctly,
    even with no charset specified.
    """
    content = "Unicode Snowman: ☃".encode("utf-8")
    headers = {"Content-Type": "text/plain"}
    response = httpx.Response(
        200,
        content=content,
        headers=headers,
    )
    assert response.text == "Unicode Snowman: ☃"
    assert response.encoding == "utf-8"


def test_response_no_charset_with_iso_8859_1_content():
    """
    A response with ISO 8859-1 encoded content should decode correctly,
    even with no charset specified, if autodetect is enabled.
    """
    content = "Accented: Österreich abcdefghijklmnopqrstuzwxyz".encode("iso-8859-1")
    headers = {"Content-Type": "text/plain"}
    response = httpx.Response(
        200, content=content, headers=headers, default_encoding=autodetect
    )
    assert response.text == "Accented: Österreich abcdefghijklmnopqrstuzwxyz"
    assert response.charset_encoding is None


def test_response_no_charset_with_cp_1252_content():
    """
    A response with Windows 1252 encoded content should decode correctly,
    even with no charset specified, if autodetect is enabled.
    """
    content = "Euro Currency: € abcdefghijklmnopqrstuzwxyz".encode("cp1252")
    headers = {"Content-Type": "text/plain"}
    response = httpx.Response(
        200, content=content, headers=headers, default_encoding=autodetect
    )
    assert response.text == "Euro Currency: € abcdefghijklmnopqrstuzwxyz"
    assert response.charset_encoding is None


def test_response_non_text_encoding():
    """
    Default to attempting utf-8 encoding for non-text content-type headers.
    """
    headers = {"Content-Type": "image/png"}
    response = httpx.Response(
        200,
        content=b"xyz",
        headers=headers,
    )
    assert response.text == "xyz"
    assert response.encoding == "utf-8"


def test_response_set_explicit_encoding():
    headers = {
        "Content-Type": "text-plain; charset=utf-8"
    }  # Deliberately incorrect charset
    response = httpx.Response(
        200,
        content="Latin 1: ÿ".encode("latin-1"),
        headers=headers,
    )
    response.encoding = "latin-1"
    assert response.text == "Latin 1: ÿ"
    assert response.encoding == "latin-1"


def test_response_force_encoding():
    response = httpx.Response(
        200,
        content="Snowman: ☃".encode("utf-8"),
    )
    response.encoding = "iso-8859-1"
    assert response.status_code == 200
    assert response.reason_phrase == "OK"
    assert response.text == "Snowman: â\x98\x83"
    assert response.encoding == "iso-8859-1"


def test_response_force_encoding_after_text_accessed():
    response = httpx.Response(
        200,
        content=b"Hello, world!",
    )
    assert response.status_code == 200
    assert response.reason_phrase == "OK"
    assert response.text == "Hello, world!"
    assert response.encoding == "utf-8"

    with pytest.raises(ValueError):
        response.encoding = "UTF8"

    with pytest.raises(ValueError):
        response.encoding = "iso-8859-1"


def test_read():
    response = httpx.Response(
        200,
        content=b"Hello, world!",
    )

    assert response.status_code == 200
    assert response.text == "Hello, world!"
    assert response.encoding == "utf-8"
    assert response.is_closed

    content = response.read()

    assert content == b"Hello, world!"
    assert response.content == b"Hello, world!"
    assert response.is_closed


def test_empty_read():
    response = httpx.Response(200)

    assert response.status_code == 200
    assert response.text == ""
    assert response.encoding == "utf-8"
    assert response.is_closed

    content = response.read()

    assert content == b""
    assert response.content == b""
    assert response.is_closed


@pytest.mark.anyio
async def test_aread():
    response = httpx.Response(
        200,
        content=b"Hello, world!",
    )

    assert response.status_code == 200
    assert response.text == "Hello, world!"
    assert response.encoding == "utf-8"
    assert response.is_closed

    content = await response.aread()

    assert content == b"Hello, world!"
    assert response.content == b"Hello, world!"
    assert response.is_closed


@pytest.mark.anyio
async def test_empty_aread():
    response = httpx.Response(200)

    assert response.status_code == 200
    assert response.text == ""
    assert response.encoding == "utf-8"
    assert response.is_closed

    content = await response.aread()

    assert content == b""
    assert response.content == b""
    assert response.is_closed


def test_iter_raw():
    response = httpx.Response(
        200,
        content=streaming_body(),
    )

    raw = b""
    for part in response.iter_raw():
        raw += part
    assert raw == b"Hello, world!"


def test_iter_raw_with_chunksize():
    response = httpx.Response(200, content=streaming_body())
    parts = list(response.iter_raw(chunk_size=5))
    assert parts == [b"Hello", b", wor", b"ld!"]

    response = httpx.Response(200, content=streaming_body())
    parts = list(response.iter_raw(chunk_size=7))
    assert parts == [b"Hello, ", b"world!"]

    response = httpx.Response(200, content=streaming_body())
    parts = list(response.iter_raw(chunk_size=13))
    assert parts == [b"Hello, world!"]

    response = httpx.Response(200, content=streaming_body())
    parts = list(response.iter_raw(chunk_size=20))
    assert parts == [b"Hello, world!"]


def test_iter_raw_doesnt_return_empty_chunks():
    def streaming_body_with_empty_chunks() -> typing.Iterator[bytes]:
        yield b"Hello, "
        yield b""
        yield b"world!"
        yield b""

    response = httpx.Response(200, content=streaming_body_with_empty_chunks())

    parts = list(response.iter_raw())
    assert parts == [b"Hello, ", b"world!"]


def test_iter_raw_on_iterable():
    response = httpx.Response(
        200,
        content=StreamingBody(),
    )

    raw = b""
    for part in response.iter_raw():
        raw += part
    assert raw == b"Hello, world!"


def test_iter_raw_on_async():
    response = httpx.Response(
        200,
        content=async_streaming_body(),
    )

    with pytest.raises(RuntimeError):
        list(response.iter_raw())


def test_close_on_async():
    response = httpx.Response(
        200,
        content=async_streaming_body(),
    )

    with pytest.raises(RuntimeError):
        response.close()


def test_iter_raw_increments_updates_counter():
    response = httpx.Response(200, content=streaming_body())

    num_downloaded = response.num_bytes_downloaded
    for part in response.iter_raw():
        assert len(part) == (response.num_bytes_downloaded - num_downloaded)
        num_downloaded = response.num_bytes_downloaded


@pytest.mark.anyio
async def test_aiter_raw():
    response = httpx.Response(200, content=async_streaming_body())

    raw = b""
    async for part in response.aiter_raw():
        raw += part
    assert raw == b"Hello, world!"


@pytest.mark.anyio
async def test_aiter_raw_with_chunksize():
    response = httpx.Response(200, content=async_streaming_body())

    parts = [part async for part in response.aiter_raw(chunk_size=5)]
    assert parts == [b"Hello", b", wor", b"ld!"]

    response = httpx.Response(200, content=async_streaming_body())

    parts = [part async for part in response.aiter_raw(chunk_size=13)]
    assert parts == [b"Hello, world!"]

    response = httpx.Response(200, content=async_streaming_body())

    parts = [part async for part in response.aiter_raw(chunk_size=20)]
    assert parts == [b"Hello, world!"]


@pytest.mark.anyio
async def test_aiter_raw_on_sync():
    response = httpx.Response(
        200,
        content=streaming_body(),
    )

    with pytest.raises(RuntimeError):
        [part async for part in response.aiter_raw()]


@pytest.mark.anyio
async def test_aclose_on_sync():
    response = httpx.Response(
        200,
        content=streaming_body(),
    )

    with pytest.raises(RuntimeError):
        await response.aclose()


@pytest.mark.anyio
async def test_aiter_raw_increments_updates_counter():
    response = httpx.Response(200, content=async_streaming_body())

    num_downloaded = response.num_bytes_downloaded
    async for part in response.aiter_raw():
        assert len(part) == (response.num_bytes_downloaded - num_downloaded)
        num_downloaded = response.num_bytes_downloaded


def test_iter_bytes():
    response = httpx.Response(200, content=b"Hello, world!")

    content = b""
    for part in response.iter_bytes():
        content += part
    assert content == b"Hello, world!"


def test_iter_bytes_with_chunk_size():
    response = httpx.Response(200, content=streaming_body())
    parts = list(response.iter_bytes(chunk_size=5))
    assert parts == [b"Hello", b", wor", b"ld!"]

    response = httpx.Response(200, content=streaming_body())
    parts = list(response.iter_bytes(chunk_size=13))
    assert parts == [b"Hello, world!"]

    response = httpx.Response(200, content=streaming_body())
    parts = list(response.iter_bytes(chunk_size=20))
    assert parts == [b"Hello, world!"]


def test_iter_bytes_with_empty_response():
    response = httpx.Response(200, content=b"")
    parts = list(response.iter_bytes())
    assert parts == []


def test_iter_bytes_doesnt_return_empty_chunks():
    def streaming_body_with_empty_chunks() -> typing.Iterator[bytes]:
        yield b"Hello, "
        yield b""
        yield b"world!"
        yield b""

    response = httpx.Response(200, content=streaming_body_with_empty_chunks())

    parts = list(response.iter_bytes())
    assert parts == [b"Hello, ", b"world!"]


@pytest.mark.anyio
async def test_aiter_bytes():
    response = httpx.Response(
        200,
        content=b"Hello, world!",
    )

    content = b""
    async for part in response.aiter_bytes():
        content += part
    assert content == b"Hello, world!"


@pytest.mark.anyio
async def test_aiter_bytes_with_chunk_size():
    response = httpx.Response(200, content=async_streaming_body())
    parts = [part async for part in response.aiter_bytes(chunk_size=5)]
    assert parts == [b"Hello", b", wor", b"ld!"]

    response = httpx.Response(200, content=async_streaming_body())
    parts = [part async for part in response.aiter_bytes(chunk_size=13)]
    assert parts == [b"Hello, world!"]

    response = httpx.Response(200, content=async_streaming_body())
    parts = [part async for part in response.aiter_bytes(chunk_size=20)]
    assert parts == [b"Hello, world!"]


def test_iter_text():
    response = httpx.Response(
        200,
        content=b"Hello, world!",
    )

    content = ""
    for part in response.iter_text():
        content += part
    assert content == "Hello, world!"


def test_iter_text_with_chunk_size():
    response = httpx.Response(200, content=b"Hello, world!")
    parts = list(response.iter_text(chunk_size=5))
    assert parts == ["Hello", ", wor", "ld!"]

    response = httpx.Response(200, content=b"Hello, world!!")
    parts = list(response.iter_text(chunk_size=7))
    assert parts == ["Hello, ", "world!!"]

    response = httpx.Response(200, content=b"Hello, world!")
    parts = list(response.iter_text(chunk_size=7))
    assert parts == ["Hello, ", "world!"]

    response = httpx.Response(200, content=b"Hello, world!")
    parts = list(response.iter_text(chunk_size=13))
    assert parts == ["Hello, world!"]

    response = httpx.Response(200, content=b"Hello, world!")
    parts = list(response.iter_text(chunk_size=20))
    assert parts == ["Hello, world!"]


@pytest.mark.anyio
async def test_aiter_text():
    response = httpx.Response(
        200,
        content=b"Hello, world!",
    )

    content = ""
    async for part in response.aiter_text():
        content += part
    assert content == "Hello, world!"


@pytest.mark.anyio
async def test_aiter_text_with_chunk_size():
    response = httpx.Response(200, content=b"Hello, world!")
    parts = [part async for part in response.aiter_text(chunk_size=5)]
    assert parts == ["Hello", ", wor", "ld!"]

    response = httpx.Response(200, content=b"Hello, world!")
    parts = [part async for part in response.aiter_text(chunk_size=13)]
    assert parts == ["Hello, world!"]

    response = httpx.Response(200, content=b"Hello, world!")
    parts = [part async for part in response.aiter_text(chunk_size=20)]
    assert parts == ["Hello, world!"]


def test_iter_lines():
    response = httpx.Response(
        200,
        content=b"Hello,\nworld!",
    )
    content = list(response.iter_lines())
    assert content == ["Hello,", "world!"]


@pytest.mark.anyio
async def test_aiter_lines():
    response = httpx.Response(
        200,
        content=b"Hello,\nworld!",
    )

    content = []
    async for line in response.aiter_lines():
        content.append(line)
    assert content == ["Hello,", "world!"]


def byte_by_byte(content: bytes) -> typing.Iterator[bytes]:
    for i in range(len(content)):
        yield content[i : i + 1]


async def async_byte_by_byte(content: bytes) -> typing.AsyncIterator[bytes]:
    for i in range(len(content)):
        yield content[i : i + 1]


JSON_CASES = [
    # A single JSON text, with top-level arrays returned element by element.
    ("application/json", b'{"a": 1}', [{"a": 1}]),
    ("application/json", b'[1, "two", {"three": [3]}]', [1, "two", {"three": [3]}]),
    ("application/json", b"[]", []),
    ("application/json", b" \r\n\t[ 1 , 2 ] \n", [1, 2]),
    ("application/json", b'[[1, [2]], {"a": "]}"}, "\\"[{"]', [[1, [2]], {"a": "]}"}, '"[{']),
    ("application/json", b"[1.5e3, -0, true, false, null]", [1500.0, 0, True, False, None]),
    ("application/json", b'"text"', ["text"]),
    ("application/json", b"1", [1]),
    ("application/json", b"\xef\xbb\xbf[1]", [1]),
    ("application/json", b" \xef\xbb\xbf {}", [{}]),
    ("application/json; charset=utf-8", b"\xef\xbb\xbf[1]", [1]),
    ("Application/JSON; Charset=UTF-8", b"[1]", [1]),
    ("application/vnd.api+json", b'{"data": []}', [{"data": []}]),
    ("APPLICATION/GEO+JSON", b'[{"type": "Point"}]', [{"type": "Point"}]),
    # Newline delimited JSON.
    ("application/x-ndjson", b'{"a": 1}\n{"b": 2}\n', [{"a": 1}, {"b": 2}]),
    ("application/ndjson", b'{"a": 1}\r\n\r\n \t\r{"b": 2}\n\n[3]', [{"a": 1}, {"b": 2}, [3]]),
    ("application/ndjson", b' 1 \n"two"\nnull', [1, "two", None]),
    ("application/ndjson", b"\xef\xbb\xbf{}\n[]", [{}, []]),
    ("application/ndjson; charset=utf-8", b"\n \n\xef\xbb\xbf{}\n[]", [{}, []]),
    ("application/ndjson", b"", []),
    ("application/ndjson", b"\n \r\n", []),
    # JSON text sequences.
    ("application/json-seq", b'\x1e{"a": 1}\n\x1e[2]\n', [{"a": 1}, [2]]),
    ("application/json-seq", b' \n\x1e1\x1e 2 \n\n\x1e"three"', [1, 2, "three"]),
    ("application/json-seq", b"\x1e\x1e\n\x1e \n\x1e1\n", [1]),
    ("application/json-seq", b"", []),
    ("application/json-seq", b" \r\n\t", []),
]


JSON_DECODING_ERROR_CASES = [
    ("application/json", b""),
    ("application/json", b" \n "),
    ("application/json", b"\xef\xbb\xbf"),
    ("application/json; charset=utf-8", b"\xef\xbb\xbf\xef\xbb\xbf[1]"),
    ("application/json", b'{"a": 1} {"b": 2}'),
    ("application/json", b'{"a": 1'),
    ("application/json", b"[1, 2] 3"),
    ("application/json", b"[1, 2]]"),
    ("application/json", b"[1, 2,]"),
    ("application/json", b"[,]"),
    ("application/json", b"[1 2]"),
    ("application/json", b"[1, 2"),
    ("application/json", b'[1, "two'),
    ("application/json", b"[1, tru]"),
    ("application/json", b"[1, 2x]"),
    ("application/json", b"[1.]"),
    ("application/json", b"[1, {]"),
    ("application/json", b'[1, "\\x"]'),
    ("application/json", b'["\xff"]'),
    ("application/ndjson", b'{"a": 1} {"b": 2}\n'),
    ("application/ndjson", b'{"a":\n1}\n'),
    ("application/ndjson", b"{}\nnope\n"),
    ("application/ndjson", b"{}\n\xef\xbb\xbf{}\n"),
    ("application/ndjson", b" \xef\xbb\xbf{}\n"),
    ("application/json-seq", b"1\n"),
    ("application/json-seq", b" 1\x1e2\n"),
    ("application/json-seq", b"\x1e"),
    ("application/json-seq", b"\x1e\n"),
    ("application/json-seq", b"\x1e \n"),
    ("application/json-seq", b"\x1e1\n\x1e"),
    ("application/json-seq", b"\x1e1 2\n"),
    ("application/json-seq", b"\x1enope\n"),
]


@pytest.mark.parametrize("content_type,content,expected", JSON_CASES)
def test_iter_json(content_type, content, expected):
    headers = {"Content-Type": content_type}
    response = httpx.Response(200, headers=headers, content=content)
    assert list(response.iter_json()) == expected

    response = httpx.Response(200, headers=headers, content=byte_by_byte(content))
    assert list(response.iter_json()) == expected


@pytest.mark.anyio
@pytest.mark.parametrize("content_type,content,expected", JSON_CASES)
async def test_aiter_json(content_type, content, expected):
    headers = {"Content-Type": content_type}
    response = httpx.Response(200, headers=headers, content=content)
    assert [value async for value in response.aiter_json()] == expected

    stream = async_byte_by_byte(content)
    response = httpx.Response(200, headers=headers, content=stream)
    assert [value async for value in response.aiter_json()] == expected


@pytest.mark.parametrize("content_type,content", JSON_DECODING_ERROR_CASES)
def test_iter_json_decoding_error(content_type, content):
    headers = {"Content-Type": content_type}
    response = httpx.Response(200, headers=headers, content=content)
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())

    response = httpx.Response(200, headers=headers, content=byte_by_byte(content))
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())


@pytest.mark.parametrize(
    "content_type",
    [
        "text/plain",
        "text/json",
        "image/svg+json",
        "application/x-json",
        "application/json-patch",
        "application/octet-stream",
    ],
)
def test_iter_json_unsupported_content_type(content_type):
    response = httpx.Response(200, headers={"Content-Type": content_type}, content=b"[1]")
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())


def test_iter_json_without_content_type():
    request = httpx.Request("GET", "https://www.example.org")
    response = httpx.Response(200, content=b"[1]", request=request)
    with pytest.raises(httpx.DecodingError) as exc_info:
        list(response.iter_json())
    assert exc_info.value.request is request


@pytest.mark.parametrize("charset", ["unknown", "base64", '""', "ü"])
def test_iter_json_invalid_charset(charset):
    content_type = f"application/json; charset={charset}".encode("utf-8")
    headers = [(b"Content-Type", content_type)]
    response = httpx.Response(200, headers=headers, content=b"[1]")
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())


@pytest.mark.parametrize(
    "content_type,encoding",
    [
        ("application/json; charset=utf-16", "utf-16"),
        ("application/json; charset=ISO-8859-1", "iso-8859-1"),
        ("application/json", "utf-16"),
        ("application/json", "utf-16-le"),
        ("application/json", "utf-16-be"),
        ("application/json", "utf-32"),
        ("application/json", "utf-32-le"),
        ("application/json", "utf-32-be"),
    ],
)
def test_iter_json_encodings(content_type, encoding):
    content = '["caf\u00e9", 1]'.encode(encoding)
    headers = {"Content-Type": content_type}
    response = httpx.Response(200, headers=headers, content=byte_by_byte(content))
    assert list(response.iter_json()) == ["caf\u00e9", 1]


def test_iter_json_with_content_encoding():
    content = gzip.compress(b'{"a": 1}\n{"b": 2}\n')
    headers = {"Content-Type": "application/x-ndjson", "Content-Encoding": "gzip"}
    response = httpx.Response(200, headers=headers, content=byte_by_byte(content))
    assert list(response.iter_json()) == [{"a": 1}, {"b": 2}]


def test_iter_json_with_json_response():
    response = httpx.Response(200, json=[{"a": 1}, [2]])
    assert list(response.iter_json()) == [{"a": 1}, [2]]


def test_iter_json_is_incremental():
    headers = {"Content-Type": "application/json"}
    response = httpx.Response(200, headers=headers, content=byte_by_byte(b"[1, 2, 3]"))
    values = response.iter_json()

    assert next(values) == 1
    assert not response.is_closed
    assert list(values) == [2, 3]
    assert response.is_closed


def test_iter_json_streaming_response_is_consumed():
    headers = {"Content-Type": "application/x-ndjson"}
    response = httpx.Response(200, headers=headers, content=byte_by_byte(b"1\n2\n"))
    assert list(response.iter_json()) == [1, 2]
    assert response.is_stream_consumed
    assert response.is_closed

    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_json())


def test_iter_json_in_memory_response_is_repeatable():
    headers = {"Content-Type": "application/x-ndjson"}
    response = httpx.Response(200, headers=headers, content=b"1\n2\n")
    assert list(response.iter_json()) == [1, 2]
    assert list(response.iter_json()) == [1, 2]

    response = httpx.Response(200, headers=headers, content=byte_by_byte(b"1\n2\n"))
    response.read()
    assert list(response.iter_json()) == [1, 2]
    assert list(response.iter_json()) == [1, 2]


def test_iter_json_decoding_error_closes_response():
    headers = {"Content-Type": "application/x-ndjson"}
    content = byte_by_byte(b'{"a": 1}\nnope\n{"b": 2}\n')
    response = httpx.Response(200, headers=headers, content=content)
    values = response.iter_json()

    assert next(values) == {"a": 1}
    with pytest.raises(httpx.DecodingError):
        next(values)
    assert response.is_closed

    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_json())


def test_iter_json_unsupported_content_type_does_not_read_response():
    headers = {"Content-Type": "text/plain"}
    response = httpx.Response(200, headers=headers, content=byte_by_byte(b"[1]"))
    with pytest.raises(httpx.DecodingError):
        list(response.iter_json())

    assert not response.is_stream_consumed
    assert response.read() == b"[1]"


@pytest.mark.anyio
async def test_aiter_json_streaming_response_is_consumed():
    headers = {"Content-Type": "application/json-seq"}
    stream = async_byte_by_byte(b"\x1e1\n\x1e2\n")
    response = httpx.Response(200, headers=headers, content=stream)
    assert [value async for value in response.aiter_json()] == [1, 2]
    assert response.is_stream_consumed
    assert response.is_closed

    with pytest.raises(httpx.StreamConsumed):
        [value async for value in response.aiter_json()]


@pytest.mark.anyio
async def test_aiter_json_in_memory_response_is_repeatable():
    headers = {"Content-Type": "application/json-seq"}
    response = httpx.Response(200, headers=headers, content=b"\x1e1\n\x1e2\n")
    assert [value async for value in response.aiter_json()] == [1, 2]
    assert [value async for value in response.aiter_json()] == [1, 2]


@pytest.mark.anyio
# Trio warns when the partially consumed `aiter_bytes()` generator is collected.
@pytest.mark.filterwarnings("ignore:Async generator:ResourceWarning")
async def test_aiter_json_decoding_error_closes_response():
    headers = {"Content-Type": "application/json"}
    stream = async_byte_by_byte(b"[1, nope, 3]")
    response = httpx.Response(200, headers=headers, content=stream)
    with pytest.raises(httpx.DecodingError):
        [value async for value in response.aiter_json()]
    assert response.is_closed

    with pytest.raises(httpx.StreamConsumed):
        [value async for value in response.aiter_json()]


@pytest.mark.anyio
async def test_aiter_json_unsupported_content_type():
    headers = {"Content-Type": "image/svg+json"}
    response = httpx.Response(200, headers=headers, content=b"[1]")
    with pytest.raises(httpx.DecodingError):
        [value async for value in response.aiter_json()]


def test_sync_streaming_response():
    response = httpx.Response(
        200,
        content=streaming_body(),
    )

    assert response.status_code == 200
    assert not response.is_closed

    content = response.read()

    assert content == b"Hello, world!"
    assert response.content == b"Hello, world!"
    assert response.is_closed


@pytest.mark.anyio
async def test_async_streaming_response():
    response = httpx.Response(
        200,
        content=async_streaming_body(),
    )

    assert response.status_code == 200
    assert not response.is_closed

    content = await response.aread()

    assert content == b"Hello, world!"
    assert response.content == b"Hello, world!"
    assert response.is_closed


def test_cannot_read_after_stream_consumed():
    response = httpx.Response(
        200,
        content=streaming_body(),
    )

    content = b""
    for part in response.iter_bytes():
        content += part

    with pytest.raises(httpx.StreamConsumed):
        response.read()


@pytest.mark.anyio
async def test_cannot_aread_after_stream_consumed():
    response = httpx.Response(
        200,
        content=async_streaming_body(),
    )

    content = b""
    async for part in response.aiter_bytes():
        content += part

    with pytest.raises(httpx.StreamConsumed):
        await response.aread()


def test_cannot_read_after_response_closed():
    response = httpx.Response(
        200,
        content=streaming_body(),
    )

    response.close()
    with pytest.raises(httpx.StreamClosed):
        response.read()


@pytest.mark.anyio
async def test_cannot_aread_after_response_closed():
    response = httpx.Response(
        200,
        content=async_streaming_body(),
    )

    await response.aclose()
    with pytest.raises(httpx.StreamClosed):
        await response.aread()


@pytest.mark.anyio
async def test_elapsed_not_available_until_closed():
    response = httpx.Response(
        200,
        content=async_streaming_body(),
    )

    with pytest.raises(RuntimeError):
        response.elapsed  # noqa: B018


def test_unknown_status_code():
    response = httpx.Response(
        600,
    )
    assert response.status_code == 600
    assert response.reason_phrase == ""
    assert response.text == ""


def test_json_with_specified_encoding():
    data = {"greeting": "hello", "recipient": "world"}
    content = json.dumps(data).encode("utf-16")
    headers = {"Content-Type": "application/json, charset=utf-16"}
    response = httpx.Response(
        200,
        content=content,
        headers=headers,
    )
    assert response.json() == data


def test_json_with_options():
    data = {"greeting": "hello", "recipient": "world", "amount": 1}
    content = json.dumps(data).encode("utf-16")
    headers = {"Content-Type": "application/json, charset=utf-16"}
    response = httpx.Response(
        200,
        content=content,
        headers=headers,
    )
    assert response.json(parse_int=str)["amount"] == "1"


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
def test_json_without_specified_charset(encoding):
    data = {"greeting": "hello", "recipient": "world"}
    content = json.dumps(data).encode(encoding)
    headers = {"Content-Type": "application/json"}
    response = httpx.Response(
        200,
        content=content,
        headers=headers,
    )
    assert response.json() == data


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
def test_json_with_specified_charset(encoding):
    data = {"greeting": "hello", "recipient": "world"}
    content = json.dumps(data).encode(encoding)
    headers = {"Content-Type": f"application/json; charset={encoding}"}
    response = httpx.Response(
        200,
        content=content,
        headers=headers,
    )
    assert response.json() == data


@pytest.mark.parametrize(
    "headers, expected",
    [
        (
            {"Link": "<https://example.com>; rel='preload'"},
            {"preload": {"rel": "preload", "url": "https://example.com"}},
        ),
        (
            {"Link": '</hub>; rel="hub", </resource>; rel="self"'},
            {
                "hub": {"url": "/hub", "rel": "hub"},
                "self": {"url": "/resource", "rel": "self"},
            },
        ),
    ],
)
def test_link_headers(headers, expected):
    response = httpx.Response(
        200,
        content=None,
        headers=headers,
    )
    assert response.links == expected


@pytest.mark.parametrize("header_value", (b"deflate", b"gzip", b"br"))
def test_decode_error_with_request(header_value):
    headers = [(b"Content-Encoding", header_value)]
    broken_compressed_body = b"xxxxxxxxxxxxxx"
    with pytest.raises(httpx.DecodingError):
        httpx.Response(
            200,
            headers=headers,
            content=broken_compressed_body,
        )

    with pytest.raises(httpx.DecodingError):
        httpx.Response(
            200,
            headers=headers,
            content=broken_compressed_body,
            request=httpx.Request("GET", "https://www.example.org/"),
        )


@pytest.mark.parametrize("header_value", (b"deflate", b"gzip", b"br"))
def test_value_error_without_request(header_value):
    headers = [(b"Content-Encoding", header_value)]
    broken_compressed_body = b"xxxxxxxxxxxxxx"
    with pytest.raises(httpx.DecodingError):
        httpx.Response(200, headers=headers, content=broken_compressed_body)


def test_response_with_unset_request():
    response = httpx.Response(200, content=b"Hello, world!")

    assert response.status_code == 200
    assert response.reason_phrase == "OK"
    assert response.text == "Hello, world!"
    assert not response.is_error


def test_set_request_after_init():
    response = httpx.Response(200, content=b"Hello, world!")

    response.request = httpx.Request("GET", "https://www.example.org")

    assert response.request.method == "GET"
    assert response.request.url == "https://www.example.org"


def test_cannot_access_unset_request():
    response = httpx.Response(200, content=b"Hello, world!")

    with pytest.raises(RuntimeError):
        response.request  # noqa: B018


def test_generator_with_transfer_encoding_header():
    def content() -> typing.Iterator[bytes]:
        yield b"test 123"  # pragma: no cover

    response = httpx.Response(200, content=content())
    assert response.headers == {"Transfer-Encoding": "chunked"}


def test_generator_with_content_length_header():
    def content() -> typing.Iterator[bytes]:
        yield b"test 123"  # pragma: no cover

    headers = {"Content-Length": "8"}
    response = httpx.Response(200, content=content(), headers=headers)
    assert response.headers == {"Content-Length": "8"}


def test_response_picklable():
    response = httpx.Response(
        200,
        content=b"Hello, world!",
        request=httpx.Request("GET", "https://example.org"),
    )
    pickle_response = pickle.loads(pickle.dumps(response))
    assert pickle_response.is_closed is True
    assert pickle_response.is_stream_consumed is True
    assert pickle_response.next_request is None
    assert pickle_response.stream is not None
    assert pickle_response.content == b"Hello, world!"
    assert pickle_response.status_code == 200
    assert pickle_response.request.url == response.request.url
    assert pickle_response.extensions == {}
    assert pickle_response.history == []


@pytest.mark.anyio
async def test_response_async_streaming_picklable():
    response = httpx.Response(200, content=async_streaming_body())
    pickle_response = pickle.loads(pickle.dumps(response))
    with pytest.raises(httpx.ResponseNotRead):
        pickle_response.content  # noqa: B018
    with pytest.raises(httpx.StreamClosed):
        await pickle_response.aread()
    assert pickle_response.is_stream_consumed is False
    assert pickle_response.num_bytes_downloaded == 0
    assert pickle_response.headers == {"Transfer-Encoding": "chunked"}

    response = httpx.Response(200, content=async_streaming_body())
    await response.aread()
    pickle_response = pickle.loads(pickle.dumps(response))
    assert pickle_response.is_stream_consumed is True
    assert pickle_response.content == b"Hello, world!"
    assert pickle_response.num_bytes_downloaded == 13


def test_response_decode_text_using_autodetect():
    # Ensure that a 'default_encoding="autodetect"' on the response allows for
    # encoding autodetection to be used when no "Content-Type: text/plain; charset=..."
    # info is present.
    #
    # Here we have some french text encoded with ISO-8859-1, rather than UTF-8.
    text = (
        "Non-seulement Despréaux ne se trompait pas, mais de tous les écrivains "
        "que la France a produits, sans excepter Voltaire lui-même, imprégné de "
        "l'esprit anglais par son séjour à Londres, c'est incontestablement "
        "Molière ou Poquelin qui reproduit avec l'exactitude la plus vive et la "
        "plus complète le fond du génie français."
    )
    content = text.encode("ISO-8859-1")
    response = httpx.Response(200, content=content, default_encoding=autodetect)

    assert response.status_code == 200
    assert response.reason_phrase == "OK"
    # The encoded byte string is consistent with either ISO-8859-1 or
    # WINDOWS-1252. Versions <6.0 of chardet claim the former, while chardet
    # 6.0 detects the latter.
    assert response.encoding in ("ISO-8859-1", "WINDOWS-1252")
    assert response.text == text


def test_response_decode_text_using_explicit_encoding():
    # Ensure that a 'default_encoding="..."' on the response is used for text decoding
    # when no "Content-Type: text/plain; charset=..."" info is present.
    #
    # Here we have some french text encoded with Windows-1252, rather than UTF-8.
    # https://en.wikipedia.org/wiki/Windows-1252
    text = (
        "Non-seulement Despréaux ne se trompait pas, mais de tous les écrivains "
        "que la France a produits, sans excepter Voltaire lui-même, imprégné de "
        "l'esprit anglais par son séjour à Londres, c'est incontestablement "
        "Molière ou Poquelin qui reproduit avec l'exactitude la plus vive et la "
        "plus complète le fond du génie français."
    )
    content = text.encode("cp1252")
    response = httpx.Response(200, content=content, default_encoding="cp1252")

    assert response.status_code == 200
    assert response.reason_phrase == "OK"
    assert response.encoding == "cp1252"
    assert response.text == text

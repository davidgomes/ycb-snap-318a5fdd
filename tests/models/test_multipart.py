import pytest

import httpx
from httpx._content import AsyncIteratorByteStream, IteratorByteStream


def _response(body: bytes, content_type: str) -> httpx.Response:
    return httpx.Response(
        200,
        content=body,
        headers={"Content-Type": content_type},
    )


def _parts(body: bytes, content_type: str) -> list[httpx.MultipartPart]:
    return list(_response(body, content_type).iter_multipart())


def test_parses_parts_and_ignores_preamble_and_epilogue() -> None:
    body = (
        b"preamble\r\n"
        b"--bound\r\n"
        b"Content-Type: text/plain\r\n"
        b"X-Dup: one\r\n"
        b"X-Dup: two\r\n"
        b"X-Fold: hello\r\n"
        b" world\r\n"
        b"\r\n"
        b"hello\r\n"
        b"--bound\r\n"
        b"\r\n"
        b"second\r\n"
        b"--bound--\r\n"
        b"epilogue\r\n"
    )
    parts = _parts(body, "multipart/mixed; boundary=bound")
    assert len(parts) == 2
    assert parts[0].content == b"hello"
    assert parts[0].headers["content-type"] == "text/plain"
    assert parts[0].headers.multi_items() == [
        ("content-type", "text/plain"),
        ("x-dup", "one"),
        ("x-dup", "two"),
        ("x-fold", "hello world"),
    ]
    assert parts[1].content == b"second"
    assert list(parts[1].headers.multi_items()) == []


def test_boundary_parsing() -> None:
    body = b"--ZZ\r\n\r\nbody\r\n--ZZ--\r\n"
    assert _parts(body, 'Multipart/Form-Data; Boundary="ZZ"')[0].content == b"body"
    parts = _parts(body, "multipart/mixed; boundary=aa; boundary=ZZ")
    assert parts[0].content == b"body"
    assert _parts(body, "multipart/mixed; boundary= ZZ ")[0].content == b"body"
    assert _parts(body, 'multipart/mixed; boundary="ZZ"')[0].content == b"body"


@pytest.mark.parametrize(
    "content_type",
    [
        "text/plain",
        "multipart/mixed",
        "multipart/; boundary=ZZ",
        "multipart/mixed; boundary=",
        "multipart/mixed; boundary==ZZ",
        'multipart/mixed; boundary="=ZZ"',
        "multipart/mixed; boundary=ZZ\x00",
        "multipart/mixed; boundary=ZZ\r\n",
        "multipart/mixed;\r\n boundary=ZZ",
    ],
)
def test_invalid_content_type(content_type: str) -> None:
    with pytest.raises(httpx.DecodingError):
        _parts(b"--ZZ--\r\n", content_type)


def test_non_ascii_boundary_is_rejected() -> None:
    response = httpx.Response(
        200,
        content=b"--ZZ--\r\n",
        headers=[
            (b"content-type", "multipart/mixed; boundary=Z\xff".encode("latin-1"))
        ],
    )
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())


def test_line_endings_and_non_delimiter_lines() -> None:
    lf = b"--b\nContent-Type: text/plain\n\nline\n--b--\n"
    cr = b"--b\r\rcr body\r--b--\r"
    assert _parts(lf, "multipart/mixed; boundary=b")[0].content == b"line"
    assert _parts(cr, "multipart/mixed; boundary=b")[0].content == b"cr body"

    almost = b"--b\r\n\r\n--bXYZ\r\n--b--\r\n"
    assert _parts(almost, "multipart/mixed; boundary=b")[0].content == b"--bXYZ"

    with pytest.raises(httpx.DecodingError):
        _parts(b"--bXYZ\r\n--b--\r\n", "multipart/mixed; boundary=b")


def test_only_closing_boundary_yields_no_parts() -> None:
    assert _parts(b"preamble\r\n--b--\r\nbye", "multipart/mixed; boundary=b") == []
    with pytest.raises(httpx.DecodingError):
        _parts(b"", "multipart/mixed; boundary=b")
    with pytest.raises(httpx.DecodingError):
        _parts(b"--b\r\n\r\nno close", "multipart/mixed; boundary=b")


@pytest.mark.parametrize(
    "body",
    [
        b"--b\r\n no-colon\r\n\r\n\r\n--b--\r\n",
        b"--b\r\n: value\r\n\r\n--b--\r\n",
        b"--b\r\nNot-A-Header\r\n\r\n--b--\r\n",
        b"--b\r\nFoo: bar\r\n \r\n\r\n--b--\r\n",
        b"--b\r\n Foo: bar\r\n\r\n--b--\r\n",
    ],
)
def test_malformed_headers(body: bytes) -> None:
    with pytest.raises(httpx.DecodingError):
        _parts(body, "multipart/mixed; boundary=b")


def test_in_memory_iteration_is_repeatable() -> None:
    response = _response(b"--b\r\n\r\nbody\r\n--b--\r\n", "multipart/mixed; boundary=b")
    first = [part.content for part in response.iter_multipart()]
    second = [part.content for part in response.iter_multipart()]
    assert first == second == [b"body"]


def test_streaming_iteration_consumes_and_closes() -> None:
    chunks = [b"--b\r", b"\n\r\nbo", b"dy\r\n--b--\r\n"]
    response = httpx.Response(
        200,
        headers={"Content-Type": "multipart/mixed; boundary=b"},
        stream=IteratorByteStream(iter(chunks)),
    )
    parts = list(response.iter_multipart())
    assert [part.content for part in parts] == [b"body"]
    assert response.is_closed
    assert response.is_stream_consumed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


def test_byte_at_a_time() -> None:
    body = b'--edge\r\nX-A: 1\r\n\r\nZZ\r\n--edge--'
    response = httpx.Response(
        200,
        headers={"Content-Type": "multipart/related; boundary=edge"},
        stream=IteratorByteStream(iter(body[i : i + 1] for i in range(len(body)))),
    )
    parts = list(response.iter_multipart())
    assert parts[0].content == b"ZZ"
    assert parts[0].headers["x-a"] == "1"


@pytest.mark.anyio
async def test_aiter_multipart_streaming() -> None:
    async def chunks():
        yield b"--b\r"
        yield b"\n\r\nasync\r\n--b--"

    response = httpx.Response(
        200,
        headers={"Content-Type": "multipart/mixed; boundary=b"},
        stream=AsyncIteratorByteStream(chunks()),
    )
    parts = [part async for part in response.aiter_multipart()]
    assert [part.content for part in parts] == [b"async"]
    assert response.is_closed
    with pytest.raises(httpx.StreamConsumed):
        [part async for part in response.aiter_multipart()]


def test_exported() -> None:
    assert httpx.MultipartPart is not None

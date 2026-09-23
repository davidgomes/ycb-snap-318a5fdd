from __future__ import annotations

import gzip
import typing

import pytest

import httpx


def _response(
    body: bytes | typing.Iterable[bytes] | typing.AsyncIterable[bytes],
    content_type: str | None,
) -> httpx.Response:
    if content_type is None:
        headers = None
    else:
        try:
            content_type.encode("ascii")
        except UnicodeEncodeError:
            headers = [(b"content-type", content_type.encode("utf-8"))]
        else:
            headers = {"Content-Type": content_type}
    return httpx.Response(200, content=body, headers=headers)


def _parts(
    body: bytes | typing.Iterable[bytes],
    content_type: str = 'multipart/mixed; boundary="bound"',
) -> list[httpx.MultipartPart]:
    return list(_response(body, content_type).iter_multipart())


def test_parses_parts_with_preamble_epilogue_and_folded_headers() -> None:
    message = (
        b"preamble line\r\n"
        b"--boundX\r\n"
        b"--bound\r\n"
        b"Content-Type: text/plain;\r\n"
        b" charset=utf-8\r\n"
        b"X-Dup: one\r\n"
        b"X-Dup: two\r\n"
        b"\r\n"
        b"hello\r"
        b"world\n"
        b"tail\r\n"
        b"--bound \t\r\n"
        b"Name: value\n"
        b"\n"
        b"second\r\n"
        b"--bound-- \r\n"
        b"epilogue --bound\r\n"
        b"still epilogue"
    )
    parts = _parts(message, "Multipart/Mixed; Boundary=bound")

    assert len(parts) == 2
    assert isinstance(parts[0], httpx.MultipartPart)
    assert isinstance(parts[0].headers, httpx.Headers)
    assert parts[0].headers["Content-Type"] == "text/plain; charset=utf-8"
    assert parts[0].headers.raw == [
        (b"Content-Type", b"text/plain; charset=utf-8"),
        (b"X-Dup", b"one"),
        (b"X-Dup", b"two"),
    ]
    assert parts[0].content == b"hello\rworld\ntail"
    assert parts[1].headers["Name"] == "value"
    assert parts[1].content == b"second"


def test_one_byte_chunks_match_buffered_parse() -> None:
    message = (
        b"preamble\n"
        b"--bound\r\n"
        b"A: 1\r\n"
        b"\r\n"
        b"abc\r\n"
        b"--bound--\n"
        b"bye"
    )
    expected = _parts(message)

    def chunks() -> typing.Iterator[bytes]:
        for index in range(len(message)):
            yield message[index : index + 1]

    assert _parts(chunks()) == expected


@pytest.mark.parametrize(
    "body",
    [
        b"--bound--",
        b"--bound--\r\n",
        b"--bound-- \t\r",
        b"preamble\r\n--bound--\ntrailer",
    ],
)
def test_closing_boundary_yields_no_parts(body: bytes) -> None:
    assert _parts(body) == []


def test_in_memory_iteration_is_repeatable() -> None:
    body = b"--bound\r\n\r\nhello\r\n--bound--\r\n"
    response = _response(body, "multipart/mixed; boundary=bound")
    assert response.content == body

    first = list(response.iter_multipart())
    second = list(response.iter_multipart())
    assert first == second
    assert first[0].content == b"hello"


def test_streaming_iteration_consumes_and_closes() -> None:
    body = b"--bound\r\nName: v\r\n\r\npayload\r\n--bound--\r\n"

    def chunks() -> typing.Iterator[bytes]:
        yield body[:8]
        yield body[8:]

    response = _response(chunks(), "multipart/form-data; boundary=bound")
    assert not response.is_closed

    parts = list(response.iter_multipart())
    assert parts[0].headers["Name"] == "v"
    assert parts[0].content == b"payload"
    assert response.is_closed
    assert response.is_stream_consumed

    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


def test_early_close_of_streaming_iteration_closes_response() -> None:
    body = b"--bound\r\n\r\none\r\n--bound\r\n\r\ntwo\r\n--bound--\r\n"

    response = _response(iter([body]), "multipart/mixed; boundary=bound")
    iterator = response.iter_multipart()
    part = next(iterator)
    assert part.content == b"one"
    iterator.close()

    assert response.is_closed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


def test_invalid_content_type_does_not_consume_stream() -> None:
    def chunks() -> typing.Iterator[bytes]:
        yield b"hello"

    response = _response(chunks(), "text/plain")
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())

    assert not response.is_stream_consumed
    assert not response.is_closed
    assert response.read() == b"hello"


def test_malformed_streaming_frame_closes_response() -> None:
    response = _response(
        iter([b"--bound\r\n\r\nhello"]),
        "multipart/mixed; boundary=bound",
    )
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())

    assert response.is_closed
    assert response.is_stream_consumed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


@pytest.mark.parametrize(
    "content_type",
    [
        None,
        "text/plain",
        "application/octet-stream",
        "multipart",
        "multipart/mixed",
        "multipart/mixed; charset=utf-8",
        "multipart/; boundary=bound",
        "MULTIPART/; boundary=bound",
        "multipart/mixed; boundary=",
        'multipart/mixed; boundary=""',
        "multipart/mixed; boundary==abc",
        'multipart/mixed; boundary="=abc"',
        "multipart/mixed; boundary=abc\x00def",
        "multipart/mixed; boundary=föo",
        'multipart/mixed; boundary="föo"',
        "multipart/mixed; boundary=bound\r\n",
        "multipart/mixed;\n boundary=bound",
        "multipart/mixed; boundary=ok; boundary=",
        "multipart/mixed; boundary=ok; boundary==nope",
    ],
)
def test_invalid_multipart_content_type(content_type: str | None) -> None:
    response = _response(b"--bound--\r\n", content_type)
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())


@pytest.mark.parametrize(
    ("content_type", "delimiter"),
    [
        ("multipart/mixed; boundary=bound", b"bound"),
        ("MULTIPART/FORM-DATA; BOUNDARY=bound", b"bound"),
        ('multipart/related; boundary="bound"', b"bound"),
        ('multipart/mixed; charset="utf-8"; boundary="a b"', b"a b"),
        ("multipart/mixed; boundary=first; boundary=second", b"second"),
        ("multipart/mixed; boundary=; boundary=recovered", b"recovered"),
        ('multipart/mixed; boundary="bo;und"', b"bo;und"),
        ("multipart/mixed; boundary = \t bound \t", b"bound"),
        ('multipart/mixed; name="föo"; boundary=bound', b"bound"),
        ('multipart/mixed; boundary="a=b"', b"a=b"),
    ],
)
def test_boundary_parsing(content_type: str, delimiter: bytes) -> None:
    body = b"--" + delimiter + b"\r\n\r\nZ\r\n--" + delimiter + b"--\r\n"
    parts = _parts(body, content_type)
    assert [part.content for part in parts] == [b"Z"]


@pytest.mark.parametrize(
    "header_block",
    [
        b"NoColon\r\n\r\n",
        b": value\r\n\r\n",
        b" Name: value\r\n\r\n",
        b"\tName: value\r\n\r\n",
        b"Name: value\r\n \r\n\r\n",
        b"Name: value\r\n\t\t\r\n\r\n",
        b"--bound--\r\n",
    ],
)
def test_malformed_part_headers(header_block: bytes) -> None:
    body = b"--bound\r\n" + header_block + b"body\r\n--bound--\r\n"
    with pytest.raises(httpx.DecodingError):
        _parts(body)


def test_header_continuation_and_duplicates() -> None:
    body = (
        b"--bound\r\n"
        b"A: one\r\n"
        b"\ttwo\r\n"
        b"A: three\r\n"
        b" four\r\n"
        b"\r\n"
        b"x\r\n"
        b"--bound--\r\n"
    )
    part = _parts(body)[0]
    assert part.headers.raw == [
        (b"A", b"one\ttwo"),
        (b"A", b"three four"),
    ]
    assert part.headers["A"] == "one\ttwo, three four"
    assert part.content == b"x"


@pytest.mark.parametrize(
    "body",
    [
        b"",
        b"just preamble\r\n",
        b"--boundX\r\n--bound\r\n\r\nZ\r\n--bound--\r\n",
        b"--bound\x0b\r\n",
        b"--bound\r\n\r\nhello\r\n",
        b"--bound\r\nName: v\r\nhello\r\n--bound--\r\n",
        b"--bound extra\r\n\r\nZ\r\n--bound--\r\n",
    ],
)
def test_malformed_framing(body: bytes) -> None:
    with pytest.raises(httpx.DecodingError):
        _parts(body)


def test_boundary_like_line_inside_body_is_content() -> None:
    body = b"--bound\r\n\r\n--boundX\r\ntext\r\n--bound--\r\n"
    assert _parts(body)[0].content == b"--boundX\r\ntext"


def test_vertical_tab_is_not_delimiter_padding() -> None:
    body = b"--bound\r\n\r\n--bound\x0b\r\n--bound--\r\n"
    assert _parts(body)[0].content == b"--bound\x0b"


def test_empty_part_and_cr_only_delimiters() -> None:
    body = b"--bound\r\r\r--bound--\r"
    assert _parts(body)[0].content == b""
    assert _parts(body)[0].headers == httpx.Headers()


def test_lf_only_message() -> None:
    body = b"--bound\nA: b\n\nxyz\n--bound--\n"
    part = _parts(body)[0]
    assert part.headers["A"] == "b"
    assert part.content == b"xyz"


def test_decoded_content_encoding_is_parsed() -> None:
    payload = b"--bound\r\n\r\nplain\r\n--bound--\r\n"
    compressed = gzip.compress(payload)

    def chunks() -> typing.Iterator[bytes]:
        yield compressed[:4]
        yield compressed[4:]

    response = httpx.Response(
        200,
        content=chunks(),
        headers={
            "Content-Type": "multipart/mixed; boundary=bound",
            "Content-Encoding": "gzip",
        },
    )
    parts = list(response.iter_multipart())
    assert parts[0].content == b"plain"
    assert response.is_closed


def test_sync_iterator_on_async_stream_raises() -> None:
    async def chunks() -> typing.AsyncIterator[bytes]:
        yield b"--bound--\r\n"

    response = _response(chunks(), "multipart/mixed; boundary=bound")
    with pytest.raises(RuntimeError):
        list(response.iter_multipart())
    assert not response.is_stream_consumed


@pytest.mark.anyio
async def test_async_multipart_iteration() -> None:
    message = b"--bound\r\nA: 1\r\n\r\none\r\n--bound\r\n\r\ntwo\r\n--bound--\r\n"

    async def chunks() -> typing.AsyncIterator[bytes]:
        for index in range(len(message)):
            yield message[index : index + 1]

    response = _response(chunks(), "multipart/mixed; boundary=bound")
    parts = [part async for part in response.aiter_multipart()]
    assert [part.content for part in parts] == [b"one", b"two"]
    assert parts[0].headers["A"] == "1"
    assert response.is_closed

    with pytest.raises(httpx.StreamConsumed):
        async for _part in response.aiter_multipart():
            pass


@pytest.mark.anyio
async def test_async_in_memory_iteration_is_repeatable() -> None:
    body = b"--bound\n\nZ\n--bound--\n"
    response = _response(body, "multipart/mixed; boundary=bound")
    first = [part async for part in response.aiter_multipart()]
    second = [part async for part in response.aiter_multipart()]
    assert first == second
    assert first[0].content == b"Z"


@pytest.mark.anyio
async def test_async_invalid_content_type_does_not_consume_stream() -> None:
    async def chunks() -> typing.AsyncIterator[bytes]:
        yield b"hello"

    response = _response(chunks(), "text/plain")
    with pytest.raises(httpx.DecodingError):
        async for _part in response.aiter_multipart():
            pass

    assert not response.is_stream_consumed
    assert await response.aread() == b"hello"

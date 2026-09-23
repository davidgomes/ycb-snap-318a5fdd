import typing

import pytest

import httpx


def _content_type(boundary: str = "b", **extra: str) -> str:
    params = "".join(f"; {name}={value}" for name, value in extra.items())
    return f"multipart/mixed; boundary={boundary}{params}"


def _response(
    body: bytes,
    content_type: str | bytes | None = None,
    *,
    chunks: list[bytes] | None = None,
    async_chunks: list[bytes] | None = None,
    request: httpx.Request | None = None,
) -> httpx.Response:
    headers: dict[str, str] | list[tuple[bytes, bytes]] | None
    if content_type is None:
        headers = None
    elif isinstance(content_type, bytes):
        headers = [(b"Content-Type", content_type)]
    else:
        headers = {"Content-Type": content_type}
    if async_chunks is not None:

        async def agen() -> typing.AsyncIterator[bytes]:
            for chunk in async_chunks:
                yield chunk

        content: bytes | typing.Iterator[bytes] | typing.AsyncIterator[bytes] = agen()
    elif chunks is not None:

        def gen() -> typing.Iterator[bytes]:
            yield from chunks

        content = gen()
    else:
        content = body
    return httpx.Response(200, headers=headers, content=content, request=request)


def _part(
    content: bytes,
    headers: list[tuple[str, str]] | None = None,
) -> httpx.MultipartPart:
    return httpx.MultipartPart(headers or [], content)


def test_multipart_part_eq_and_repr() -> None:
    part = httpx.MultipartPart([("Content-Type", "text/plain")], b"hello")
    same = httpx.MultipartPart(httpx.Headers({"Content-Type": "text/plain"}), b"hello")
    assert part == same
    assert part != _part(b"other", [("Content-Type", "text/plain")])
    assert part != "hello"
    assert isinstance(part.headers, httpx.Headers)
    assert "MultipartPart" in repr(part)
    assert part.headers["content-type"] == "text/plain"
    assert part.content == b"hello"


def test_parses_parts_and_ignores_preamble_and_epilogue() -> None:
    body = (
        b"preamble line\r\n"
        b"--bEXTRA\r\n"
        b"--b\r\n"
        b"Content-Type: text/plain\r\n"
        b"X-Dup: one\r\n"
        b"X-Dup: two\r\n"
        b"\r\n"
        b"hello\r\n"
        b"world\r\n"
        b"--b\r\n"
        b"\r\n"
        b"\x00\xff\r\n"
        b"--b--\r\n"
        b"epilogue\r\n"
        b"--b\r\n"
        b"\r\n"
        b"ignored\r\n"
        b"--b--\r\n"
    )
    response = _response(body, _content_type())
    parts = list(response.iter_multipart())

    assert parts == [
        _part(
            b"hello\r\nworld",
            [
                ("Content-Type", "text/plain"),
                ("X-Dup", "one"),
                ("X-Dup", "two"),
            ],
        ),
        _part(b"\x00\xff"),
    ]
    assert parts[0].headers.multi_items() == [
        ("content-type", "text/plain"),
        ("x-dup", "one"),
        ("x-dup", "two"),
    ]
    assert parts[0].headers.raw[0] == (b"Content-Type", b"text/plain")
    assert parts[0].headers["x-dup"] == "one, two"
    # Buffered bodies stay repeatable after the stream has been read.
    assert response.is_stream_consumed
    assert list(response.iter_multipart()) == parts


@pytest.mark.parametrize(
    "body, expected",
    [
        (b"--b\nContent-Type: text/plain\n\nhello\nworld\n--b--\n", b"hello\nworld"),
        (b"--b\rContent-Type: text/plain\r\rhello\rworld\r--b--\r", b"hello\rworld"),
        (b"--b\r\n\r\nhello\nworld\r\n--b--\r\n", b"hello\nworld"),
        (b"--b \t\r\n\r\nZ\r\n--b-- \t\r\n", b"Z"),
        (b"--b\r\n\r\nhello\r\n--b--", b"hello"),
        (b"--b--", b""),
        (b"--b--\r", b""),
    ],
)
def test_line_endings_padding_and_closing_boundary(
    body: bytes, expected: bytes
) -> None:
    parts = list(_response(body, _content_type()).iter_multipart())
    if body.startswith(b"--b--"):
        assert parts == []
    else:
        assert [part.content for part in parts] == [expected]


def test_boundary_like_lines_are_content_after_the_preamble() -> None:
    body = b"--a-\r\n\r\n--a--\r\nZ\r\n--a---\r\n"
    parts = list(_response(body, _content_type(boundary="a-")).iter_multipart())
    assert parts == [_part(b"--a--\r\nZ")]


def test_header_continuation_appends_to_previous_value() -> None:
    body = (
        b"--b\r\n"
        b"Content-Type: text/plain;\r\n"
        b"\tcharset=utf-8\r\n"
        b"X-Empty:\r\n"
        b"X-Note: a b:c\r\n"
        b"\r\n"
        b"body\r\n"
        b"--b--\r\n"
    )
    part = list(_response(body, _content_type()).iter_multipart())[0]
    assert part.headers["content-type"] == "text/plain;\tcharset=utf-8"
    assert part.headers["x-empty"] == ""
    assert part.headers["x-note"] == "a b:c"
    assert part.content == b"body"


def test_only_a_closing_boundary_yields_no_parts() -> None:
    body = b"preamble\r\n--b--\r\nepilogue"
    assert list(_response(body, _content_type()).iter_multipart()) == []


def test_empty_part_is_yielded() -> None:
    body = b"--b\r\n\r\n--b--\r\n"
    parts = list(_response(body, _content_type()).iter_multipart())
    assert parts == [_part(b"")]
    assert list(parts[0].headers.multi_items()) == []


@pytest.mark.parametrize(
    "content_type",
    [
        None,
        "text/plain",
        "text/plain; boundary=b",
        "noslash",
        "multipart",
        "multipart/",
        "multipart/ ; boundary=b",
        "multipart/\t; boundary=b",
        "multipart/mixed",
        "multipart/mixed;",
        "multipart/mixed; charset=utf-8",
        "multipart/mixed; boundary=",
        'multipart/mixed; boundary=""',
        'multipart/mixed; boundary="  "',
        'multipart/mixed; boundary="foo',
        "multipart/mixed; boundary==foo",
        'multipart/mixed; boundary="=foo"',
        "multipart/mixed; boundary=foo\x00bar",
        'multipart/mixed; boundary="foo\x00"',
        "multipart/mixed; boundary=föo".encode(),
        'multipart/mixed; boundary="föo"'.encode(),
        b"multipart/mixed; boundary=f\xf6o",
        "multipart/mixed; boundary=b\r\n",
        "multipart/mixed;\n boundary=b",
        "multipart/mixed; boundary=b\r",
    ],
)
def test_invalid_content_type_raises_decoding_error(
    content_type: str | bytes | None,
) -> None:
    response = _response(b"--b--\r\n", content_type)
    with pytest.raises(httpx.DecodingError):
        response.iter_multipart()
    # Invalid metadata does not consume a streaming body. This response is
    # buffered, so a second attempt still fails the same way.
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())


def test_invalid_content_type_does_not_consume_stream() -> None:
    def chunks() -> typing.Iterator[bytes]:
        yield b"still here"

    response = httpx.Response(
        200,
        headers={"Content-Type": "text/plain"},
        content=chunks(),
    )
    with pytest.raises(httpx.DecodingError):
        response.iter_multipart()
    assert response.is_stream_consumed is False
    assert response.is_closed is False
    assert response.read() == b"still here"


@pytest.mark.parametrize(
    "content_type, boundary",
    [
        ("multipart/mixed; boundary=b", "b"),
        ("Multipart/Form-Data; Boundary=b", "b"),
        ("MULTIPART/RELATED; BOUNDARY=b", "b"),
        ('multipart/mixed; boundary="b"', "b"),
        ('multipart/mixed; boundary = "b"', "b"),
        ("multipart/mixed; boundary= b ", "b"),
        ("multipart/mixed; boundary=\tb\t", "b"),
        ('multipart/mixed; boundary=" b "', "b"),
        ("multipart/mixed; charset=utf-8; boundary=b", "b"),
        ("multipart/mixed; boundary=first; boundary=b", "b"),
        ('multipart/mixed; boundary="a;b"; boundary=b', "b"),
        ("multipart/mixed; foo; boundary=b", "b"),
        ("multipart/mixed; ; boundary=b;", "b"),
        ('multipart/mixed; boundary="foo\\"bar"', 'foo"bar'),
        # A trailing backslash escapes the closing quote, so the value keeps it.
        ('multipart/mixed; boundary="foo\\"', "foo\\"),
    ],
)
def test_boundary_parameter_parsing(content_type: str, boundary: str) -> None:
    body = f"--{boundary}\r\n\r\nok\r\n--{boundary}--\r\n".encode()
    parts = list(_response(body, content_type).iter_multipart())
    assert parts == [_part(b"ok")]


@pytest.mark.parametrize(
    "body",
    [
        b"",
        b"just preamble\r\n",
        b"--bEXTRA\r\n--b\r\n\r\nok\r\n--b--\r\n",
        b"--b\r\nNotAHeader\r\n\r\n--b--\r\n",
        b"--b\r\n: value\r\n\r\n--b--\r\n",
        b"--b\r\n value: no\r\n\r\n--b--\r\n",
        b"--b\r\nName: v\r\n \r\n\r\n--b--\r\n",
        b"--b\r\nName: v\r\n\t\t\r\n\r\n--b--\r\n",
        b"--b\r\n\r\nok\r\n--b\r\n",
        b"--b\r\n--b--\r\n",
    ],
)
def test_malformed_framing_and_headers(body: bytes) -> None:
    response = _response(body, _content_type())
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())
    # Buffered malformed bodies remain repeatable.
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())


def test_streaming_iteration_consumes_and_closes() -> None:
    chunks = [b"--b\r\n\r\nhello\r", b"\n--b--\r\n", b"epilogue"]
    response = _response(b"", _content_type(), chunks=chunks)
    assert response.is_closed is False

    parts = list(response.iter_multipart())
    assert parts == [_part(b"hello")]
    assert response.is_stream_consumed is True
    assert response.is_closed is True
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


def test_streaming_one_byte_chunks_and_early_close() -> None:
    body = b"--b\r\nContent-Type: text/plain\r\n\r\nhello\r\n--b--\r\n"
    chunks = [body[i : i + 1] for i in range(len(body))]
    chunks.append(b"epilogue")
    response = _response(body, _content_type(), chunks=chunks)
    iterator = response.iter_multipart()
    assert isinstance(iterator, typing.Generator)
    part = next(iterator)
    assert part.content == b"hello"
    assert part.headers["content-type"] == "text/plain"
    iterator.close()
    assert response.is_closed is True
    with pytest.raises(httpx.StreamConsumed):
        next(response.iter_multipart())


def test_streaming_framing_error_closes_response() -> None:
    response = _response(
        b"",
        _content_type(),
        chunks=[b"--b\r\n\r\nhello\r\n", b"more"],
    )
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())
    assert response.is_closed is True
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


def test_decoding_error_includes_request() -> None:
    request = httpx.Request("GET", "https://www.example.org/")
    response = _response(b"nope", _content_type(), request=request)
    with pytest.raises(httpx.DecodingError) as exc_info:
        list(response.iter_multipart())
    assert exc_info.value.request is request


@pytest.mark.anyio
async def test_aiter_multipart_buffered_is_repeatable() -> None:
    body = b"--b\r\n\r\nhello\r\n--b--\r\n"
    response = _response(body, _content_type())
    first = [part async for part in response.aiter_multipart()]
    second = [part async for part in response.aiter_multipart()]
    assert first == second == [_part(b"hello")]

    # The closing delimiter is only completed when the buffer is flushed.
    unterminated = _response(b"--b\r\n\r\nhello\r\n--b--", _content_type())
    flushed = [part async for part in unterminated.aiter_multipart()]
    assert flushed == [_part(b"hello")]


@pytest.mark.anyio
async def test_aiter_multipart_streaming_consumes_stream() -> None:
    chunks = [b"--b\r\n", b"\r\nhello\r\n--b--\r\n", b"tail"]
    response = _response(b"", _content_type(), async_chunks=chunks)
    parts = [part async for part in response.aiter_multipart()]
    assert parts == [_part(b"hello")]
    assert response.is_stream_consumed is True
    assert response.is_closed is True
    with pytest.raises(httpx.StreamConsumed):
        [part async for part in response.aiter_multipart()]

    # Closing delimiter split from its terminator, completed by flush.
    flushed = _response(
        b"",
        _content_type(),
        async_chunks=[b"--b\r\n\r\nhello\r\n", b"--b--"],
    )
    assert [part async for part in flushed.aiter_multipart()] == [_part(b"hello")]


@pytest.mark.anyio
async def test_aiter_invalid_content_type_is_eager() -> None:
    response = _response(b"--b--", "text/plain")
    with pytest.raises(httpx.DecodingError):
        response.aiter_multipart()


@pytest.mark.anyio
async def test_aiter_streaming_framing_error_closes() -> None:
    response = _response(
        b"",
        _content_type(),
        async_chunks=[b"--bEXTRA\r\n", b"rest"],
    )
    with pytest.raises(httpx.DecodingError):
        [part async for part in response.aiter_multipart()]
    assert response.is_closed is True
    with pytest.raises(httpx.StreamConsumed):
        [part async for part in response.aiter_multipart()]

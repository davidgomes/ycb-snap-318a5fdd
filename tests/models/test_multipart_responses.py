from __future__ import annotations

import gzip
import typing

import pytest

import httpx


def multipart_response(
    body: bytes,
    content_type: str | bytes = "multipart/mixed; boundary=abc",
) -> httpx.Response:
    if isinstance(content_type, str):
        content_type = content_type.encode("ascii")
    return httpx.Response(200, headers={b"Content-Type": content_type}, content=body)


def iter_chunks(body: bytes, chunk_size: int) -> typing.Iterator[bytes]:
    for i in range(0, len(body), chunk_size):
        yield body[i : i + chunk_size]


async def aiter_chunks(body: bytes, chunk_size: int) -> typing.AsyncIterator[bytes]:
    for i in range(0, len(body), chunk_size):
        yield body[i : i + chunk_size]


def parse(
    body: bytes, content_type: str | bytes = "multipart/mixed; boundary=abc"
) -> list[httpx.MultipartPart]:
    return list(multipart_response(body, content_type).iter_multipart())


BODY = (
    b"This is the preamble.\r\n"
    b"--abc\r\n"
    b"Content-Type: text/plain\r\n"
    b"Content-ID: <one>\r\n"
    b"\r\n"
    b"Hello, world!\r\n"
    b"--abc\r\n"
    b"Content-Type: application/json\r\n"
    b"\r\n"
    b'{"hello": "world"}\r\n'
    b"--abc--\r\n"
    b"This is the epilogue.\r\n"
)

PARTS = [
    httpx.MultipartPart(
        headers={"Content-Type": "text/plain", "Content-ID": "<one>"},
        content=b"Hello, world!",
    ),
    httpx.MultipartPart(
        headers={"Content-Type": "application/json"},
        content=b'{"hello": "world"}',
    ),
]


def test_iter_multipart():
    parts = parse(BODY)
    assert parts == PARTS
    assert parts[0].headers.raw == [
        (b"Content-Type", b"text/plain"),
        (b"Content-ID", b"<one>"),
    ]


@pytest.mark.anyio
async def test_aiter_multipart():
    response = multipart_response(BODY)
    parts = [part async for part in response.aiter_multipart()]
    assert parts == PARTS


@pytest.mark.anyio
async def test_aiter_multipart_with_unterminated_close_delimiter():
    response = multipart_response(b"--abc\r\n\r\nhello\r\n--abc--")
    parts = [part async for part in response.aiter_multipart()]
    assert parts == [httpx.MultipartPart(headers={}, content=b"hello")]


@pytest.mark.parametrize("newline", [b"\n", b"\r", b"\r\n"])
def test_line_terminators(newline: bytes) -> None:
    body = BODY.replace(b"\r\n", newline)
    assert parse(body) == PARTS


def test_mixed_line_terminators_within_content_are_preserved():
    body = b"--abc\n\r\na\r\nb\nc\rd\r\n\r\n--abc--"
    assert parse(body) == [httpx.MultipartPart(headers={}, content=b"a\r\nb\nc\rd\r\n")]


@pytest.mark.parametrize(
    "body",
    [
        BODY,
        BODY.replace(b"\r\n", b"\n"),
        BODY.replace(b"\r\n", b"\r"),
        b"--abc\r\r\n\r\r\nx\r\r\n--abc--",
    ],
)
def test_parsing_is_independent_of_chunk_boundaries(body: bytes) -> None:
    expected = parse(body)
    for chunk_size in range(1, len(body) + 1):
        response = httpx.Response(
            200,
            headers={"Content-Type": "multipart/mixed; boundary=abc"},
            content=iter_chunks(body, chunk_size),
        )
        assert list(response.iter_multipart()) == expected


def test_crlf_split_across_chunks():
    response = httpx.Response(
        200,
        headers={"Content-Type": "multipart/mixed; boundary=abc"},
        content=iter([b"--abc\r", b"\n\r", b"\nhello\r", b"\n--abc--\r", b"\n"]),
    )
    assert list(response.iter_multipart()) == [
        httpx.MultipartPart(headers={}, content=b"hello")
    ]


@pytest.mark.parametrize(
    "content_type,boundary",
    [
        ("multipart/mixed; boundary=abc", b"abc"),
        ("MULTIPART/Mixed; BOUNDARY=abc", b"abc"),
        ("multipart/byteranges; boundary=abc", b"abc"),
        ('multipart/mixed; boundary="abc"', b"abc"),
        ("multipart/mixed; boundary= \tabc\t ", b"abc"),
        ('multipart/mixed; boundary = "abc" ', b"abc"),
        ('multipart/mixed; boundary="a b:c"', b"a b:c"),
        ("multipart/mixed; boundary=xyz; boundary=abc", b"abc"),
        ('multipart/related; boundary=abc; start-info="x;boundary=xyz"', b"abc"),
        ("multipart/mixed; charset=utf-8; boundary=abc; foo=bar", b"abc"),
        ("multipart/mixed; boundary=abc=", b"abc="),
    ],
)
def test_boundary_parsing(content_type: str, boundary: bytes) -> None:
    body = b"--" + boundary + b"\r\n\r\nhello\r\n--" + boundary + b"--"
    assert parse(body, content_type) == [
        httpx.MultipartPart(headers={}, content=b"hello")
    ]


@pytest.mark.parametrize(
    "content_type",
    [
        "text/plain; boundary=abc",
        "multipart; boundary=abc",
        "multipart/; boundary=abc",
        "multipart/ ; boundary=abc",
        "multipartx/mixed; boundary=abc",
        "multipart/mixed",
        "multipart/mixed; charset=utf-8",
        "multipart/mixed; boundary",
        "multipart/mixed; boundary=",
        "multipart/mixed; boundary= \t ",
        'multipart/mixed; boundary=""',
        "multipart/mixed; boundary==abc",
        'multipart/mixed; boundary="=abc"',
        "multipart/mixed; boundary=a\x00c",
        b"multipart/mixed; boundary=\xc3\xa9",
        b"multipart/mixed; boundary=\xe9",
        "multipart/mixed; boundary=abc; boundary=",
        "multipart/mixed; boundary=abc\r\n",
        "multipart/mixed;\r\n boundary=abc",
        "multipart/mixed;\n boundary=abc",
        "multipart/mixed; boundary=a\rc",
        "multipart/mixed; boundary=abc; charset=\nutf-8",
    ],
)
def test_invalid_content_type(content_type: str | bytes) -> None:
    response = multipart_response(b"--abc\r\n\r\nhello\r\n--abc--", content_type)
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())


def test_missing_content_type():
    response = httpx.Response(200, content=b"--abc\r\n\r\nhello\r\n--abc--")
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())


def test_invalid_content_type_does_not_consume_stream():
    response = httpx.Response(
        200,
        headers={"Content-Type": "text/plain"},
        content=iter_chunks(b"Hello, world!", 5),
    )
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())
    assert not response.is_stream_consumed
    assert response.read() == b"Hello, world!"


@pytest.mark.parametrize("padding", [b" ", b"\t", b" \t "])
def test_delimiter_lines_allow_trailing_whitespace(padding: bytes) -> None:
    body = b"--abc" + padding + b"\r\n\r\nhello\r\n--abc--" + padding + b"\r\n"
    assert parse(body) == [httpx.MultipartPart(headers={}, content=b"hello")]


def test_boundary_like_lines_within_content():
    body = (
        b"--abc\r\n\r\n--abcd\r\n--abc x\r\n--abc---\r\n --abc\r\n-abc\r\n--abc--\r\n"
    )
    assert parse(body) == [
        httpx.MultipartPart(
            headers={}, content=b"--abcd\r\n--abc x\r\n--abc---\r\n --abc\r\n-abc"
        )
    ]


def test_boundary_like_lines_within_preamble():
    body = b"preamble\r\n--abcd\r\n--abc x\r\n--abc\r\n\r\nhello\r\n--abc--"
    assert parse(body) == [httpx.MultipartPart(headers={}, content=b"hello")]


@pytest.mark.parametrize(
    "body",
    [
        b"--abcd\r\n--abc\r\n\r\nhello\r\n--abc--",
        b"--abc x\r\n--abc\r\n\r\nhello\r\n--abc--",
        b"--abc---\r\n\r\nhello\r\n--abc--",
        b"--abc-- x",
    ],
)
def test_malformed_first_delimiter_line(body: bytes) -> None:
    with pytest.raises(httpx.DecodingError):
        parse(body)


def test_epilogue_is_ignored():
    body = b"--abc\r\n\r\nhello\r\n--abc--\r\n--abc\r\nnot a header\r\n--abc x"
    assert parse(body) == [httpx.MultipartPart(headers={}, content=b"hello")]


@pytest.mark.parametrize(
    "body",
    [
        b"--abc--",
        b"--abc--\r\n",
        b"preamble\r\n--abc--\r\nepilogue",
    ],
)
def test_close_delimiter_only(body: bytes) -> None:
    assert parse(body) == []


@pytest.mark.parametrize(
    "body",
    [
        b"",
        b"\r\n",
        b"no delimiters here",
        b"--abc",
        b"--abc\r\n",
        b"--abc\r\nContent-Type: text/plain\r\n",
        b"--abc\r\n\r\nhello",
        b"--abc\r\n\r\nhello\r\n",
        b"--abc\r\n\r\nhello\r\n--abc\r\n\r\nworld\r\n",
        b"--abc\r\n\r\nhello\r\n--abcd--",
    ],
)
def test_incomplete_body(body: bytes) -> None:
    with pytest.raises(httpx.DecodingError):
        parse(body)


def test_empty_parts():
    body = b"--abc\r\n\r\n--abc\r\n\r\n\r\n--abc\r\nX-Foo: bar\r\n\r\n--abc--"
    assert parse(body) == [
        httpx.MultipartPart(headers={}, content=b""),
        httpx.MultipartPart(headers={}, content=b""),
        httpx.MultipartPart(headers={"X-Foo": "bar"}, content=b""),
    ]


def test_content_excludes_only_the_final_line_terminator():
    body = b"--abc\r\n\r\n\r\nhello\r\n\r\n--abc--"
    assert parse(body) == [httpx.MultipartPart(headers={}, content=b"\r\nhello\r\n")]


def test_header_parsing():
    body = (
        b"--abc\r\n"
        b"X-Folded: one\r\n"
        b"  two\r\n"
        b"\tthree \r\n"
        b"X-Empty:\r\n"
        b" continued\r\n"
        b"Set-Cookie: a=1\r\n"
        b"set-cookie: b=2\r\n"
        b"X-Spaces :  value  \r\n"
        b"X-Colons: a:b:c\r\n"
        b"\r\n"
        b"hello\r\n"
        b"--abc--"
    )
    (part,) = parse(body)
    assert part.headers.raw == [
        (b"X-Folded", b"one two three"),
        (b"X-Empty", b"continued"),
        (b"Set-Cookie", b"a=1"),
        (b"set-cookie", b"b=2"),
        (b"X-Spaces", b"value"),
        (b"X-Colons", b"a:b:c"),
    ]
    assert part.headers.get_list("set-cookie") == ["a=1", "b=2"]
    assert part.content == b"hello"


def test_header_parsing_with_non_ascii_values():
    body = b"--abc\r\nX-Name: caf\xc3\xa9\r\n\r\nhello\r\n--abc--"
    (part,) = parse(body)
    assert part.headers["X-Name"] == "café"


@pytest.mark.parametrize(
    "header_lines",
    [
        b"no colon here\r\n",
        b": empty name\r\n",
        b" \t: empty name\r\n",
        b" X-Foo: leading whitespace\r\n",
        b"\tX-Foo: leading whitespace\r\n",
        b"X-Foo: bar\r\n \t \r\n",
        b"X-Foo: bar\r\n \r\n",
        b"X-Foo: bar\r\n--abc--\r\n",
    ],
)
def test_malformed_headers(header_lines: bytes) -> None:
    body = b"--abc\r\n" + header_lines + b"\r\nhello\r\n--abc--"
    with pytest.raises(httpx.DecodingError):
        parse(body)


def test_malformed_header_in_later_part():
    response = httpx.Response(
        200,
        headers={"Content-Type": "multipart/mixed; boundary=abc"},
        content=iter([b"--abc\r\n\r\nhello\r\n--abc\r\nbad\r\n\r\n--abc--"]),
    )
    parts = []
    with pytest.raises(httpx.DecodingError):
        for part in response.iter_multipart():
            parts.append(part)
    assert parts == [httpx.MultipartPart(headers={}, content=b"hello")]


def test_streaming_response_is_consumed_and_closed():
    response = httpx.Response(
        200,
        headers={"Content-Type": "multipart/mixed; boundary=abc"},
        content=iter_chunks(BODY, 7),
    )
    assert list(response.iter_multipart()) == PARTS
    assert response.is_stream_consumed
    assert response.is_closed

    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


@pytest.mark.anyio
async def test_async_streaming_response_is_consumed_and_closed():
    response = httpx.Response(
        200,
        headers={"Content-Type": "multipart/mixed; boundary=abc"},
        content=aiter_chunks(BODY, 7),
    )
    assert [part async for part in response.aiter_multipart()] == PARTS
    assert response.is_stream_consumed
    assert response.is_closed

    with pytest.raises(httpx.StreamConsumed):
        [part async for part in response.aiter_multipart()]


MALFORMED_BODIES = [
    b"--abc\r\nbad header\r\n\r\nhello\r\n--abc--\r\nepilogue",
    b"--abcd\r\n\r\nhello\r\n--abc--\r\nepilogue",
    b"--abc\r\n\r\nhello\r\n--abc\r\n: empty name\r\n\r\n--abc--\r\nepilogue",
    b"--abc\r\n\r\nhello\r\n--abc\r\n\r\nno closing delimiter\r\n",
]


@pytest.mark.parametrize("body", MALFORMED_BODIES)
@pytest.mark.parametrize("chunk_size", [1, 4, 1024])
def test_streaming_response_is_consumed_and_closed_on_malformed_body(
    body: bytes, chunk_size: int
) -> None:
    response = httpx.Response(
        200,
        headers={"Content-Type": "multipart/mixed; boundary=abc"},
        content=iter_chunks(body, chunk_size),
    )
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())
    assert response.num_bytes_downloaded == len(body)
    assert response.is_closed

    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


@pytest.mark.anyio
@pytest.mark.parametrize("body", MALFORMED_BODIES)
@pytest.mark.parametrize("chunk_size", [1, 4, 1024])
async def test_async_streaming_response_is_consumed_and_closed_on_malformed_body(
    body: bytes, chunk_size: int
) -> None:
    response = httpx.Response(
        200,
        headers={"Content-Type": "multipart/mixed; boundary=abc"},
        content=aiter_chunks(body, chunk_size),
    )
    with pytest.raises(httpx.DecodingError):
        [part async for part in response.aiter_multipart()]
    assert response.num_bytes_downloaded == len(body)
    assert response.is_closed

    with pytest.raises(httpx.StreamConsumed):
        [part async for part in response.aiter_multipart()]


@pytest.mark.anyio
@pytest.mark.parametrize("body", MALFORMED_BODIES)
async def test_async_in_memory_malformed_body(body: bytes) -> None:
    response = multipart_response(body)
    for _ in range(2):
        with pytest.raises(httpx.DecodingError):
            [part async for part in response.aiter_multipart()]


@pytest.mark.anyio
async def test_async_invalid_content_type():
    response = httpx.Response(
        200,
        headers={"Content-Type": "text/plain"},
        content=aiter_chunks(b"Hello, world!", 5),
    )
    with pytest.raises(httpx.DecodingError):
        [part async for part in response.aiter_multipart()]
    assert not response.is_stream_consumed
    assert await response.aread() == b"Hello, world!"


def test_streaming_response_is_closed_on_content_decoding_error():
    response = httpx.Response(
        200,
        headers={
            "Content-Type": "multipart/mixed; boundary=abc",
            "Content-Encoding": "gzip",
        },
        content=iter([b"not gzip data"]),
    )
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())
    assert response.is_closed


def test_in_memory_response_is_repeatable():
    response = multipart_response(BODY)
    assert list(response.iter_multipart()) == PARTS
    assert list(response.iter_multipart()) == PARTS


@pytest.mark.anyio
async def test_async_in_memory_response_is_repeatable():
    response = multipart_response(BODY)
    assert [part async for part in response.aiter_multipart()] == PARTS
    assert [part async for part in response.aiter_multipart()] == PARTS


def test_read_streaming_response_is_repeatable():
    response = httpx.Response(
        200,
        headers={"Content-Type": "multipart/mixed; boundary=abc"},
        content=iter_chunks(BODY, 7),
    )
    response.read()
    assert list(response.iter_multipart()) == PARTS
    assert list(response.iter_multipart()) == PARTS


def test_content_encoding_is_decoded():
    response = httpx.Response(
        200,
        headers={
            "Content-Type": "multipart/mixed; boundary=abc",
            "Content-Encoding": "gzip",
        },
        content=gzip.compress(BODY),
    )
    assert list(response.iter_multipart()) == PARTS


def test_decoding_error_includes_request():
    request = httpx.Request("GET", "https://example.org")
    response = httpx.Response(
        200,
        headers={"Content-Type": "text/plain"},
        content=b"Hello, world!",
        request=request,
    )
    with pytest.raises(httpx.DecodingError) as exc_info:
        list(response.iter_multipart())
    assert exc_info.value.request is request


def test_client_multipart_response():
    def handler(request: httpx.Request) -> httpx.Response:
        return multipart_response(BODY)

    with httpx.Client(transport=httpx.MockTransport(handler)) as client:
        with client.stream("GET", "https://example.org") as response:
            assert list(response.iter_multipart()) == PARTS


def test_multipart_part():
    part = httpx.MultipartPart(
        headers=httpx.Headers({"Content-Type": "text/plain"}), content=b"hello"
    )
    assert isinstance(part.headers, httpx.Headers)
    assert part.headers["content-type"] == "text/plain"
    assert part.content == b"hello"
    assert part == httpx.MultipartPart({"Content-Type": "text/plain"}, b"hello")
    assert part != httpx.MultipartPart({"Content-Type": "text/html"}, b"hello")
    assert part != httpx.MultipartPart({"Content-Type": "text/plain"}, b"world")
    assert part != b"hello"
    assert repr(part) == (
        "MultipartPart(headers=Headers({'content-type': 'text/plain'}), "
        "content=b'hello')"
    )

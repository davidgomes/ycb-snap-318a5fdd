import typing

import pytest

import httpx

CONTENT_TYPE = "multipart/mixed; boundary=abc"


def parts(content: bytes, content_type: str = CONTENT_TYPE) -> list:
    response = httpx.Response(
        200, headers={"Content-Type": content_type}, content=content
    )
    return [(part.headers.multi_items(), part.content) for part in response.iter_multipart()]


def chunked_parts(chunks: typing.List[bytes]) -> list:
    response = httpx.Response(
        200,
        headers={"Content-Type": CONTENT_TYPE},
        content=iter(chunks),
    )
    return [(part.headers.multi_items(), part.content) for part in response.iter_multipart()]


BODY = (
    b"preamble\r\n"
    b"--abc\r\n"
    b"Content-Type: text/plain\r\n"
    b"\r\n"
    b"Hello\r\n"
    b"--abc\r\n"
    b"X-Thing: 1\r\n"
    b"X-Thing: 2\r\n"
    b"\r\n"
    b"line one\r\n"
    b"line two\r\n"
    b"--abc--\r\n"
    b"epilogue --abc\r\n"
)

EXPECTED = [
    ([("content-type", "text/plain")], b"Hello"),
    ([("x-thing", "1"), ("x-thing", "2")], b"line one\r\nline two"),
]


def test_iter_multipart():
    response = httpx.Response(
        200, headers={"Content-Type": CONTENT_TYPE}, content=BODY
    )
    result = list(response.iter_multipart())
    assert result == [
        httpx.MultipartPart(httpx.Headers({"Content-Type": "text/plain"}), b"Hello"),
        httpx.MultipartPart(
            httpx.Headers([("X-Thing", "1"), ("X-Thing", "2")]),
            b"line one\r\nline two",
        ),
    ]
    assert isinstance(result[0].headers, httpx.Headers)
    # In-memory content is repeatable.
    assert list(response.iter_multipart()) == result


@pytest.mark.parametrize("newline", [b"\n", b"\r", b"\r\n"])
def test_line_endings(newline):
    body = BODY.replace(b"\r\n", newline)
    expected = [
        (headers, content.replace(b"\r\n", newline)) for headers, content in EXPECTED
    ]
    assert parts(body) == expected


def test_byte_by_byte_chunks():
    assert chunked_parts([bytes([b]) for b in BODY]) == EXPECTED
    cr_body = BODY.replace(b"\r\n", b"\r")
    assert chunked_parts([bytes([b]) for b in cr_body]) == [
        (h, c.replace(b"\r\n", b"\r")) for h, c in EXPECTED
    ]


def test_crlf_split_across_chunks():
    body = b"--abc\r\n\r\nbody\r\n--abc--"
    assert chunked_parts([b"--abc\r", b"\n\r", b"\nbody\r", b"\n--abc--"]) == [
        ([], b"body")
    ]
    assert parts(body) == [([], b"body")]


def test_close_only_yields_zero_parts():
    assert parts(b"--abc--\r\n") == []
    assert parts(b"preamble\r\n--abc--") == []


@pytest.mark.parametrize(
    "body",
    [
        b"",
        b"no delimiters here\r\n",
        b"--abc\r\nX: 1\r\n",
        b"--abc\r\nX: 1\r\n\r\nbody",
        b"--abc\r\nX: 1\r\n\r\nbody\r\n--abcd--",
        b"--abcx\r\n\r\nbody\r\n--abc--",
        b"--abc-\r\n\r\nbody\r\n--abc--",
        b"--abc\r\n--abc--\r\n",
    ],
)
def test_malformed_framing(body):
    with pytest.raises(httpx.DecodingError):
        parts(body)


def test_boundary_like_lines_are_content():
    body = (
        b"x\r\n--abcx\r\n"
        b"--abc\r\n\r\n"
        b"--abcd\r\n--abc--x\r\n-- abc\r\n"
        b"--abc \t\r\n\r\nsecond\r\n--abc--\t "
    )
    assert parts(body) == [
        ([], b"--abcd\r\n--abc--x\r\n-- abc"),
        ([], b"second"),
    ]


def test_empty_body_part():
    assert parts(b"--abc\r\nX: y\r\n\r\n\r\n--abc--") == [([("x", "y")], b"")]
    assert parts(b"--abc\r\nX: y\r\n\r\n--abc--") == [([("x", "y")], b"")]


def test_header_continuation():
    body = b"--abc\r\nX: a\r\n  b\r\n\tc\r\nY:z\r\n\r\n\r\n--abc--"
    assert parts(body) == [([("x", "a b c"), ("y", "z")], b"")]


@pytest.mark.parametrize(
    "header_block",
    [
        b"no colon",
        b": empty name",
        b" X: leading whitespace",
        b"X: a\r\n \t",
    ],
)
def test_malformed_headers(header_block):
    with pytest.raises(httpx.DecodingError):
        parts(b"--abc\r\n" + header_block + b"\r\n\r\nbody\r\n--abc--")


@pytest.mark.parametrize(
    "content_type,boundary",
    [
        ("multipart/mixed; boundary=abc", b"abc"),
        ("MULTIPART/Mixed; BOUNDARY=abc", b"abc"),
        ('multipart/related; boundary="abc"', b"abc"),
        ("multipart/mixed; boundary= \tabc \t", b"abc"),
        ('multipart/mixed; boundary = "abc" ', b"abc"),
        ("multipart/mixed; boundary=zzz; boundary=abc", b"abc"),
        ('multipart/mixed; boundary="a;b"', b"a;b"),
        ("multipart/mixed; charset=utf-8; boundary=abc", b"abc"),
    ],
)
def test_boundary_parsing(content_type, boundary):
    body = b"--" + boundary + b"\r\n\r\nok\r\n--" + boundary + b"--"
    assert parts(body, content_type=content_type) == [([], b"ok")]


@pytest.mark.parametrize(
    "content_type",
    [
        "text/plain; boundary=abc",
        "multipart/; boundary=abc",
        "multipart; boundary=abc",
        "multipart/mixed",
        "multipart/mixed; boundary=",
        'multipart/mixed; boundary=""',
        "multipart/mixed; boundary==abc",
        "multipart/mixed; boundary=a\x00b",
        "multipart/mixed; boundary=abc\r\n",
        "multipart/mixed; boundary=abc; x=\n",
        "multipart/mixed; boundary=abc; boundary=",
    ],
)
def test_invalid_content_type(content_type):
    with pytest.raises(httpx.DecodingError):
        parts(b"--abc\r\n\r\nok\r\n--abc--", content_type=content_type)


def test_non_ascii_boundary():
    response = httpx.Response(
        200,
        headers=[(b"Content-Type", "multipart/mixed; boundary=ä".encode("utf-8"))],
        content=b"--\xc3\xa4\r\n\r\nok\r\n--\xc3\xa4--",
    )
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())


def test_missing_content_type():
    response = httpx.Response(200, content=b"--abc--")
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())


def test_decoding_error_has_request():
    request = httpx.Request("GET", "https://example.org")
    response = httpx.Response(200, content=b"--abc--", request=request)
    with pytest.raises(httpx.DecodingError) as exc_info:
        list(response.iter_multipart())
    assert exc_info.value.request is request


def test_streaming_multipart_is_consumed_once():
    response = httpx.Response(
        200,
        headers={"Content-Type": CONTENT_TYPE},
        stream=httpx.ByteStream(BODY),
    )
    parts = list(response.iter_multipart())
    assert len(parts) == 2
    assert response.is_closed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


@pytest.mark.anyio
async def test_aiter_multipart():
    async def body() -> typing.AsyncIterator[bytes]:
        for b in BODY:
            yield bytes([b])

    response = httpx.Response(
        200, headers={"Content-Type": CONTENT_TYPE}, content=body()
    )
    result = [
        (part.headers.multi_items(), part.content)
        async for part in response.aiter_multipart()
    ]
    assert result == EXPECTED
    assert response.is_closed
    with pytest.raises(httpx.StreamConsumed):
        [part async for part in response.aiter_multipart()]


@pytest.mark.anyio
async def test_aiter_multipart_in_memory_is_repeatable():
    response = httpx.Response(
        200, headers={"Content-Type": CONTENT_TYPE}, content=BODY
    )
    first = [part async for part in response.aiter_multipart()]
    second = [part async for part in response.aiter_multipart()]
    assert first == second
    assert len(first) == 2


@pytest.mark.anyio
async def test_aiter_multipart_malformed():
    response = httpx.Response(
        200, headers={"Content-Type": CONTENT_TYPE}, content=b"--abc\r\n"
    )
    with pytest.raises(httpx.DecodingError):
        [part async for part in response.aiter_multipart()]

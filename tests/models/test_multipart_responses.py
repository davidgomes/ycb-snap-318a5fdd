import typing

import pytest

import httpx


def multipart_response(
    content: bytes,
    content_type: str = "multipart/mixed; boundary=b",
    chunk_size: typing.Optional[int] = None,
) -> httpx.Response:
    if chunk_size is None:
        return httpx.Response(
            200, headers={"Content-Type": content_type}, content=content
        )

    def stream() -> typing.Iterator[bytes]:
        for i in range(0, len(content), chunk_size):
            yield content[i : i + chunk_size]

    return httpx.Response(200, headers={"Content-Type": content_type}, content=stream())


def async_multipart_response(
    content: bytes, content_type: str = "multipart/mixed; boundary=b"
) -> httpx.Response:
    async def stream() -> typing.AsyncIterator[bytes]:
        for i in range(len(content)):
            yield content[i : i + 1]

    return httpx.Response(200, headers={"Content-Type": content_type}, content=stream())


def parse(content: bytes, **kwargs: typing.Any) -> typing.List[httpx.MultipartPart]:
    return list(multipart_response(content, **kwargs).iter_multipart())


BASIC_BODY = (
    b"preamble\r\n"
    b"--b\r\n"
    b"Content-Type: text/plain\r\n"
    b"\r\n"
    b"hello\r\n"
    b"--b\r\n"
    b"Content-Type: application/json\r\n"
    b"X-Dup: 1\r\n"
    b"X-Dup: 2\r\n"
    b"\r\n"
    b'{"a": 1}\r\n'
    b"--b--\r\n"
    b"epilogue\r\n"
)


def expected_basic_parts() -> typing.List[httpx.MultipartPart]:
    return [
        httpx.MultipartPart(
            headers=httpx.Headers({"Content-Type": "text/plain"}), content=b"hello"
        ),
        httpx.MultipartPart(
            headers=httpx.Headers(
                [
                    ("Content-Type", "application/json"),
                    ("X-Dup", "1"),
                    ("X-Dup", "2"),
                ]
            ),
            content=b'{"a": 1}',
        ),
    ]


@pytest.mark.parametrize("chunk_size", [None, 1, 2, 3, 7])
def test_iter_multipart(chunk_size: typing.Optional[int]) -> None:
    parts = parse(BASIC_BODY, chunk_size=chunk_size)
    assert parts == expected_basic_parts()
    assert parts[1].headers.get_list("x-dup") == ["1", "2"]


@pytest.mark.anyio
async def test_aiter_multipart() -> None:
    response = async_multipart_response(BASIC_BODY)
    parts = [part async for part in response.aiter_multipart()]
    assert parts == expected_basic_parts()
    assert response.is_closed


@pytest.mark.parametrize("newline", [b"\n", b"\r", b"\r\n"])
@pytest.mark.parametrize("chunk_size", [None, 1])
def test_line_terminators(newline: bytes, chunk_size: typing.Optional[int]) -> None:
    body = newline.join(
        [b"--b", b"A: 1", b"", b"line1", b"line2", b"--b", b"", b"x", b"--b--", b""]
    )
    parts = parse(body, chunk_size=chunk_size)
    assert [part.content for part in parts] == [b"line1" + newline + b"line2", b"x"]
    assert parts[0].headers["a"] == "1"
    assert len(parts[1].headers) == 0


def test_crlf_split_across_chunks() -> None:
    body = b"--b\r\n\r\nabc\r\n--b--"

    def stream() -> typing.Iterator[bytes]:
        yield b"--b\r"
        yield b"\n\r"
        yield b"\nabc\r"
        yield b"\n--b--"

    response = httpx.Response(
        200, headers={"Content-Type": "multipart/mixed; boundary=b"}, content=stream()
    )
    assert [part.content for part in response.iter_multipart()] == [b"abc"]
    assert [part.content for part in parse(body)] == [b"abc"]


def test_body_excludes_only_final_terminator() -> None:
    parts = parse(b"--b\r\n\r\n\r\n\r\n--b--")
    assert [part.content for part in parts] == [b"\r\n"]
    parts = parse(b"--b\r\n\r\n\r\n--b--")
    assert [part.content for part in parts] == [b""]
    parts = parse(b"--b\r\n\r\n--b--")
    assert [part.content for part in parts] == [b""]


def test_mixed_terminators_preserved_in_body() -> None:
    parts = parse(b"--b\n\na\rb\r\nc\n--b--")
    assert [part.content for part in parts] == [b"a\rb\r\nc"]


def test_delimiter_trailing_whitespace() -> None:
    parts = parse(b"--b \t\r\n\r\ndata\r\n--b-- \t\r\n")
    assert [part.content for part in parts] == [b"data"]


def test_boundary_like_lines_are_content() -> None:
    body = b"pre\r\n--bx\r\n--b\r\n\r\n--bx\r\n--b---\r\n --b\r\n--b\r\n\r\nz\r\n--b--"
    parts = parse(body)
    assert [part.content for part in parts] == [b"--bx\r\n--b---\r\n --b", b"z"]


def test_message_starting_with_non_delimiter_boundary_line() -> None:
    with pytest.raises(httpx.DecodingError):
        parse(b"--bogus\r\n--b\r\n\r\nx\r\n--b--")
    with pytest.raises(httpx.DecodingError):
        parse(b"--b--x\r\n")


def test_only_closing_boundary_yields_zero_parts() -> None:
    assert parse(b"--b--") == []
    assert parse(b"preamble\n--b--\nepilogue") == []


@pytest.mark.parametrize(
    "body",
    [
        b"",
        b"no delimiters here",
        b"--b\r\n\r\ntruncated",
        b"--b\r\n\r\nabc\r\n--b\r\n",
        b"--b",
    ],
)
def test_malformed_framing(body: bytes) -> None:
    with pytest.raises(httpx.DecodingError):
        parse(body)


def test_epilogue_is_ignored() -> None:
    parts = parse(b"--b\r\n\r\nx\r\n--b--\r\n--b\r\nnot a header\r\n")
    assert [part.content for part in parts] == [b"x"]


def test_header_continuation() -> None:
    parts = parse(b"--b\r\nX-Long: a\r\n  b\r\n\tc\r\nY: 1\r\n\r\n\r\n--b--")
    assert parts[0].headers.multi_items() == [("x-long", "a b c"), ("y", "1")]


@pytest.mark.parametrize(
    "header_block",
    [
        b"no colon",
        b": empty name",
        b" Leading: whitespace",
        b"\tLeading: whitespace",
        b"A: 1\r\n  ",
        b"A: 1\r\n\t",
    ],
)
def test_malformed_headers(header_block: bytes) -> None:
    with pytest.raises(httpx.DecodingError):
        parse(b"--b\r\n" + header_block + b"\r\n\r\nx\r\n--b--")


def test_header_whitespace_and_non_ascii() -> None:
    parts = parse(b"--b\r\nA:   spaced \t\r\nB:\r\n\r\n\r\n--b--")
    assert parts[0].headers.multi_items() == [("a", "spaced"), ("b", "")]
    parts = parse("--b\r\nName: caf\u00e9\r\n\r\n\r\n--b--".encode("utf-8"))
    assert parts[0].headers["name"] == "caf\u00e9"


@pytest.mark.parametrize(
    "content_type,boundary",
    [
        ("multipart/mixed; boundary=abc", b"abc"),
        ("MULTIPART/Related; BOUNDARY=abc", b"abc"),
        ('multipart/mixed; boundary="abc def"', b"abc def"),
        ("multipart/mixed; boundary= \tabc \t", b"abc"),
        ('multipart/mixed; boundary = "abc"', b"abc"),
        ("multipart/mixed; boundary=first; boundary=last", b"last"),
        ('multipart/mixed; foo="a;boundary=x"; boundary=abc', b"abc"),
        ('multipart/mixed; foo="a\\";boundary=x"; boundary=abc', b"abc"),
        ("multipart/byteranges;boundary=abc;charset=utf-8", b"abc"),
    ],
)
def test_valid_boundary(content_type: str, boundary: bytes) -> None:
    body = b"--" + boundary + b"\r\n\r\ndata\r\n--" + boundary + b"--\r\n"
    parts = parse(body, content_type=content_type)
    assert [part.content for part in parts] == [b"data"]


@pytest.mark.parametrize(
    "content_type",
    [
        "text/plain; boundary=abc",
        "multipart/; boundary=abc",
        "multipart; boundary=abc",
        "multipart/mixed",
        "multipart/mixed; boundary=",
        'multipart/mixed; boundary=""',
        "multipart/mixed; boundary=  ",
        "multipart/mixed; boundary==abc",
        "multipart/mixed; boundary=ab\x00c",
        "multipart/mixed; boundary=caf\u00e9",
        'multipart/mixed; boundary="abc',
        "multipart/mixed; boundary=abc\r\n",
        "multipart/mixed; boundary=abc; x=\n",
        "multipart/mixed; boundary=abc; boundary=",
    ],
)
def test_invalid_content_type(content_type: str) -> None:
    response = httpx.Response(
        200,
        headers=[(b"Content-Type", content_type.encode("utf-8"))],
        content=b"--abc\r\n\r\ndata\r\n--abc--\r\n",
    )
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())


def test_missing_content_type() -> None:
    response = httpx.Response(200, content=b"--b--")
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())


def test_decoding_error_has_request() -> None:
    request = httpx.Request("GET", "https://example.org")
    response = httpx.Response(200, content=b"x", request=request)
    with pytest.raises(httpx.DecodingError) as exc_info:
        list(response.iter_multipart())
    assert exc_info.value.request is request


def test_in_memory_multipart_is_repeatable() -> None:
    response = multipart_response(BASIC_BODY)
    assert list(response.iter_multipart()) == expected_basic_parts()
    assert list(response.iter_multipart()) == expected_basic_parts()


def test_streaming_multipart_consumes_and_closes() -> None:
    response = multipart_response(BASIC_BODY, chunk_size=4)
    assert list(response.iter_multipart()) == expected_basic_parts()
    assert response.is_closed
    with pytest.raises(httpx.StreamConsumed):
        list(response.iter_multipart())


def test_streaming_multipart_closes_on_error() -> None:
    response = multipart_response(b"--b\r\nbad header\r\n\r\n--b--", chunk_size=4)
    with pytest.raises(httpx.DecodingError):
        list(response.iter_multipart())
    assert response.is_closed


def test_read_streaming_response_then_iterate_repeatedly() -> None:
    response = multipart_response(BASIC_BODY, chunk_size=4)
    response.read()
    assert list(response.iter_multipart()) == expected_basic_parts()
    assert list(response.iter_multipart()) == expected_basic_parts()


@pytest.mark.anyio
async def test_async_streaming_multipart_consumes() -> None:
    response = async_multipart_response(BASIC_BODY)
    assert [part async for part in response.aiter_multipart()] == (
        expected_basic_parts()
    )
    with pytest.raises(httpx.StreamConsumed):
        [part async for part in response.aiter_multipart()]


@pytest.mark.anyio
async def test_async_in_memory_multipart_is_repeatable() -> None:
    response = async_multipart_response(BASIC_BODY)
    await response.aread()
    for _ in range(2):
        parts = [part async for part in response.aiter_multipart()]
        assert parts == expected_basic_parts()


@pytest.mark.anyio
async def test_async_closing_delimiter_without_newline() -> None:
    response = async_multipart_response(b"--b\n\nfirst\n--b\n\nsecond\n--b--")
    parts = [part async for part in response.aiter_multipart()]
    assert [part.content for part in parts] == [b"first", b"second"]


@pytest.mark.anyio
async def test_async_invalid_multipart() -> None:
    response = async_multipart_response(b"--b\r\n\r\nno close")
    with pytest.raises(httpx.DecodingError):
        [part async for part in response.aiter_multipart()]
    assert response.is_closed


def test_multipart_with_content_encoding() -> None:
    import gzip

    response = httpx.Response(
        200,
        headers={
            "Content-Type": "multipart/mixed; boundary=b",
            "Content-Encoding": "gzip",
        },
        content=gzip.compress(BASIC_BODY),
    )
    assert list(response.iter_multipart()) == expected_basic_parts()


def test_multipart_part_repr_and_eq() -> None:
    part = httpx.MultipartPart(headers=httpx.Headers({"a": "1"}), content=b"x")
    assert repr(part) == "MultipartPart(headers=Headers({'a': '1'}), content=b'x')"
    assert part == httpx.MultipartPart(headers={"a": "1"}, content=b"x")
    assert part != httpx.MultipartPart(headers={"a": "1"}, content=b"y")
    assert part != object()

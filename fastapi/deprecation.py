from datetime import datetime, timezone
from email.utils import format_datetime
from typing import Any

from starlette.responses import Response


def first_not_none(*values: Any) -> Any:
    for value in values:
        if value is not None:
            return value
    return None


def format_rfc7231_datetime(value: datetime) -> str:
    if value.tzinfo is None:
        value = value.replace(tzinfo=timezone.utc)
    return format_datetime(value.astimezone(timezone.utc), usegmt=True)


def successor_version_link(url: str) -> str:
    return f'<{url}>; rel="successor-version"'


def apply_deprecation_headers(
    response: Response,
    *,
    deprecated: bool | None,
    sunset: datetime | None,
    deprecation_date: datetime | None,
    successor_url: str | None,
) -> None:
    headers = response.headers
    if deprecation_date is not None:
        if "deprecation" not in headers:
            headers["deprecation"] = format_rfc7231_datetime(deprecation_date)
    elif deprecated:
        if "deprecation" not in headers:
            headers["deprecation"] = "true"
    if sunset is not None and "sunset" not in headers:
        headers["sunset"] = format_rfc7231_datetime(sunset)
    if successor_url is not None:
        new_link = successor_version_link(successor_url)
        existing = headers.get("link")
        if existing:
            headers["link"] = f"{existing}, {new_link}"
        else:
            headers["link"] = new_link

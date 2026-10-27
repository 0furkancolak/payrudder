"""Request building, retry policy and error mapping shared by the sync and the async client."""

from __future__ import annotations

import json
import os
import random
from collections.abc import Mapping
from dataclasses import dataclass
from datetime import date, datetime
from decimal import Decimal
from typing import Any

import httpx

from . import _wire
from ._version import __version__
from .errors import (
    APIConnectionError,
    APIError,
    APITimeoutError,
    ConfigurationError,
    error_class,
)
from .types import RequestOptions

DEFAULT_BASE_URL = "http://localhost:42800"
DEFAULT_TIMEOUT = 30.0
DEFAULT_MAX_RETRIES = 2
BACKOFF_BASE = 0.5
BACKOFF_CAP = 8.0
# A Retry-After longer than this is not waited for: the caller gets the 429 and decides.
MAX_RETRY_AFTER = 60.0

_IDEMPOTENT_METHODS = frozenset({"GET", "HEAD", "PUT", "DELETE", "OPTIONS"})
_RETRY_ALWAYS = frozenset({429, 502, 503, 504})


@dataclass(frozen=True)
class Settings:
    api_key: str
    base_url: str
    timeout: float
    max_retries: int
    user_agent: str
    default_headers: Mapping[str, str]


def resolve_settings(
    api_key: str | None,
    base_url: str | None,
    timeout: float,
    max_retries: int,
    user_agent: str | None,
    default_headers: Mapping[str, str] | None,
) -> Settings:
    key = api_key or os.environ.get("PAYRUDDER_API_KEY")
    if not key:
        raise ConfigurationError("no API key: pass api_key=... or set PAYRUDDER_API_KEY")
    if max_retries < 0:
        raise ConfigurationError("max_retries must not be negative")
    url = (base_url or os.environ.get("PAYRUDDER_BASE_URL") or DEFAULT_BASE_URL).rstrip("/")
    return Settings(
        key, url, timeout, max_retries, user_agent or f"payrudder-python/{__version__}", dict(default_headers or {})
    )


@dataclass
class Prepared:
    method: str
    url: str
    headers: dict[str, str]
    params: httpx.QueryParams
    content: bytes | None
    files: list[tuple[str, tuple[str, bytes, str]]] | None
    data: dict[str, str] | None
    timeout: float
    max_retries: int
    idempotent: bool
    returns: str
    response_wire: Any


def query_value(value: Any) -> str:
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, (datetime, date, Decimal)):
        return str(_wire.encode(value))
    return str(value)


def has_idempotency(body: Any, headers: Mapping[str, str]) -> bool:
    """True when a POST can be repeated without doing its work twice: a header key, a body key, or a key per event."""
    if "Idempotency-Key" in headers:
        return True
    if isinstance(body, dict):
        if body.get("idempotency_key"):
            return True
        events = body.get("events")
        if isinstance(events, list) and events:
            return all(isinstance(e, dict) and e.get("idempotency_key") for e in events)
    return False


def prepare(
    settings: Settings,
    method: str,
    path: str,
    *,
    query: Mapping[str, Any] | None,
    body: Any,
    body_wire: Any,
    files: Any,
    form: Mapping[str, Any] | None,
    returns: str,
    response_wire: Any,
    options: RequestOptions | None,
) -> Prepared:
    opts = options or {}
    headers = {
        "Authorization": f"Bearer {settings.api_key}",
        "User-Agent": settings.user_agent,
        "Accept": "application/json" if returns == "json" else "*/*",
        **settings.default_headers,
        **(opts.get("headers") or {}),
    }
    if "idempotency_key" in opts:
        headers["Idempotency-Key"] = opts["idempotency_key"]
    content: bytes | None = None
    if body is not None:
        content = json.dumps(_wire.encode(body, body_wire), separators=(",", ":"), ensure_ascii=False).encode()
        headers["Content-Type"] = "application/json"
    pairs: list[tuple[str, str | int | float | bool | None]] = [
        (k, query_value(v)) for k, v in (query or {}).items() if v is not None
    ]
    params = httpx.QueryParams(pairs)
    multipart = None
    data = None
    if files is not None:
        multipart = [("file", (f[0], f[1], f[2] if len(f) > 2 else "application/octet-stream")) for f in files]
        if form:
            data = {"metadata": json.dumps(_wire.encode(form), separators=(",", ":"))}
    idempotent = method in _IDEMPOTENT_METHODS or has_idempotency(body, headers)
    return Prepared(
        method=method,
        url=settings.base_url + path,
        headers=headers,
        params=params,
        content=content,
        files=multipart,
        data=data,
        timeout=opts.get("timeout", settings.timeout),
        max_retries=opts.get("max_retries", settings.max_retries),
        idempotent=idempotent,
        returns=returns,
        response_wire=response_wire,
    )


def backoff_delay(attempt: int, retry_after: float | None) -> float:
    """Seconds to wait before retry number ``attempt + 1``: Retry-After when sent, else exponential with jitter."""
    if retry_after is not None:
        return max(0.0, retry_after)
    ceiling = min(BACKOFF_CAP, BACKOFF_BASE * 2.0**attempt)
    return ceiling * random.uniform(0.5, 1.0)


def status_retriable(status: int, idempotent: bool, retry_after: float | None) -> bool:
    if retry_after is not None and retry_after > MAX_RETRY_AFTER:
        return False
    if status in _RETRY_ALWAYS:
        return True
    return status == 500 and idempotent


def transport_retriable(exc: httpx.TransportError, idempotent: bool) -> bool:
    """Retry a connection that never opened; one that may have carried the request only if the call can repeat."""
    if isinstance(exc, (httpx.ConnectError, httpx.ConnectTimeout, httpx.PoolTimeout)):
        return True
    if isinstance(exc, (httpx.TimeoutException, httpx.NetworkError, httpx.RemoteProtocolError)):
        return idempotent
    return False


def connection_error(exc: httpx.TransportError) -> APIConnectionError:
    if isinstance(exc, httpx.TimeoutException):
        return APITimeoutError(f"request timed out: {exc}")
    return APIConnectionError(f"connection failed: {exc}")


def parse_retry_after(headers: Mapping[str, str]) -> float | None:
    raw = headers.get("retry-after")
    if raw is None:
        return None
    try:
        return max(0.0, float(raw.strip()))
    except ValueError:
        return None  # an HTTP-date form is not used by the gateway


def error_from_response(response: httpx.Response) -> APIError:
    status = response.status_code
    headers = {k.lower(): v for k, v in response.headers.items()}
    body: Any
    try:
        body = response.json()
    except ValueError:
        body = None
    envelope = body.get("error") if isinstance(body, dict) else None
    retry_after = parse_retry_after(headers)
    if isinstance(envelope, dict):
        details = envelope.get("details")
        return error_class(status)(
            str(envelope.get("message") or response.reason_phrase or f"HTTP {status}"),
            status_code=status,
            code=str(envelope.get("code") or ""),
            key=str(envelope.get("key") or ""),
            details=details if isinstance(details, list) else None,
            request_id=str(envelope.get("request_id") or headers.get("x-request-id") or "") or None,
            retry_after=retry_after,
            headers=headers,
            body=body,
        )
    snippet = response.text[:200].strip()
    return error_class(status)(
        snippet or response.reason_phrase or f"HTTP {status}",
        status_code=status,
        code="UNKNOWN",
        key="common.unknown",
        request_id=headers.get("x-request-id"),
        retry_after=retry_after,
        headers=headers,
        body=body if body is not None else snippet,
    )


def parse_success(response: httpx.Response, prepared: Prepared) -> Any:
    if prepared.returns == "text":
        return response.text
    if prepared.returns == "bytes":
        return response.content
    if response.status_code == 204 or not response.content:
        return None
    try:
        data = response.json()
    except ValueError as exc:
        raise APIError(
            "the API answered with a body that is not JSON",
            status_code=response.status_code,
            code="INVALID_RESPONSE",
            key="common.invalid_response",
            request_id=response.headers.get("x-request-id"),
        ) from exc
    return _wire.decode(data, prepared.response_wire)

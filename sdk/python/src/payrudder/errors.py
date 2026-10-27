"""Errors raised by the SDK.

The API always answers the ``Error`` envelope ``{"error": {"code", "key", "message", "details", "request_id"}}``.
``APIError`` carries every part of it; the subclasses are picked by HTTP status.
"""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any


class PayrudderError(Exception):
    """Base class of everything this package raises."""


class ConfigurationError(PayrudderError, ValueError):
    """The client was built without what it needs (an API key)."""


class APIConnectionError(PayrudderError):
    """No usable answer: DNS, connection, TLS or protocol failure (after the retries)."""


class APITimeoutError(APIConnectionError):
    """The request timed out (after the retries)."""


class APIError(PayrudderError):
    """The API answered with an error status."""

    def __init__(
        self,
        message: str,
        *,
        status_code: int,
        code: str = "",
        key: str = "",
        details: list[dict[str, Any]] | None = None,
        request_id: str | None = None,
        retry_after: float | None = None,
        headers: Mapping[str, str] | None = None,
        body: Any = None,
    ) -> None:
        super().__init__(message)
        self.message = message
        self.status_code = status_code
        self.code = code
        """gRPC-style code such as ``INVALID_ARGUMENT``."""
        self.key = key
        """Stable, translatable key such as ``billing.credit_note.nothing_to_refund``."""
        self.details = details or []
        """Per-field problems: ``[{"field": ..., "description": ...}]``."""
        self.request_id = request_id
        self.retry_after = retry_after
        """Seconds from the ``Retry-After`` header, when present."""
        self.headers = dict(headers or {})
        self.body = body

    def __str__(self) -> str:
        head = f"{self.status_code} {self.code}".strip()
        key = f" [{self.key}]" if self.key else ""
        rid = f" (request {self.request_id})" if self.request_id else ""
        return f"{head}{key}: {self.message}{rid}"


class AuthenticationError(APIError):
    """401: the API key is missing, malformed, expired or revoked."""


class PermissionDeniedError(APIError):
    """403: the key lacks the permission, the tenant is not active, or a sandbox-only route got a live key."""


class NotFoundError(APIError):
    """404."""


class ConflictError(APIError):
    """409: the state does not allow it (already exists, wrong status, idempotency conflict)."""


class ValidationError(APIError):
    """400 or 422: the request was rejected; ``details`` names the fields."""


class RateLimitError(APIError):
    """429: honour ``retry_after``."""


class ServerError(APIError):
    """5xx."""


def error_class(status: int) -> type[APIError]:
    if status == 401:
        return AuthenticationError
    if status == 403:
        return PermissionDeniedError
    if status == 404:
        return NotFoundError
    if status == 409:
        return ConflictError
    if status in (400, 422):
        return ValidationError
    if status == 429:
        return RateLimitError
    if status >= 500:
        return ServerError
    return APIError


class WebhookSignatureError(PayrudderError):
    """A webhook delivery failed verification (bad, missing or stale signature)."""

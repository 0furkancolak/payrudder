"""Small public types shared by the client and the generated resources."""

from __future__ import annotations

from collections.abc import Mapping
from typing import TypedDict

# (filename, content) or (filename, content, content_type). Bytes only: a request must be replayable for retries.
FileUpload = tuple[str, bytes] | tuple[str, bytes, str]


class RequestOptions(TypedDict, total=False):
    """Per-call overrides accepted by every resource method as ``options=``."""

    timeout: float
    """Seconds for this call (connect, read, write and pool each)."""
    max_retries: int
    """Retries for this call instead of the client default (0 disables them)."""
    idempotency_key: str
    """Sent as the ``Idempotency-Key`` header. Also makes a POST safe to retry after a 500 or a lost connection."""
    headers: Mapping[str, str]
    """Extra headers for this call."""

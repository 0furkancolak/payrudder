"""The sync and async clients."""

from __future__ import annotations

import asyncio
import time
from collections.abc import Awaitable, Callable, Mapping
from types import TracebackType
from typing import Any

import httpx

from . import _core
from .resources import AsyncResources, Resources
from .types import RequestOptions


class Payrudder(Resources):
    """Synchronous client for the Payrudder tenant API (``/v1/*``).

    >>> client = Payrudder(api_key="qk_test_...")
    >>> client.customers.get_customer("cus_42")

    Args:
        api_key: ``qk_test_...`` / ``qk_live_...``; falls back to ``PAYRUDDER_API_KEY``.
        base_url: falls back to ``PAYRUDDER_BASE_URL``, then ``http://localhost:42800``.
        timeout: seconds per request (default 30).
        max_retries: automatic retries for network errors, 429, 502/503/504 and (idempotent calls) 500. Default 2.
        http_client: your own ``httpx.Client`` (not closed by this client). Mutually exclusive with ``transport``.
        transport: an ``httpx.BaseTransport`` (for tests, proxies, mTLS) used to build the internal client.
    """

    def __init__(
        self,
        api_key: str | None = None,
        *,
        base_url: str | None = None,
        timeout: float = _core.DEFAULT_TIMEOUT,
        max_retries: int = _core.DEFAULT_MAX_RETRIES,
        user_agent: str | None = None,
        default_headers: Mapping[str, str] | None = None,
        http_client: httpx.Client | None = None,
        transport: httpx.BaseTransport | None = None,
    ) -> None:
        if http_client is not None and transport is not None:
            raise ValueError("pass http_client or transport, not both")
        self._settings = _core.resolve_settings(api_key, base_url, timeout, max_retries, user_agent, default_headers)
        self._owns_http = http_client is None
        self._http = http_client or httpx.Client(transport=transport)
        self._sleep: Callable[[float], None] = time.sleep
        self._init_resources(self)

    @property
    def base_url(self) -> str:
        return self._settings.base_url

    def close(self) -> None:
        if self._owns_http:
            self._http.close()

    def __enter__(self) -> Payrudder:
        return self

    def __exit__(
        self, exc_type: type[BaseException] | None, exc: BaseException | None, tb: TracebackType | None
    ) -> None:
        self.close()

    def _request(
        self,
        method: str,
        path: str,
        *,
        query: Mapping[str, Any] | None = None,
        body: Any = None,
        body_wire: Any = None,
        files: Any = None,
        form: Mapping[str, Any] | None = None,
        returns: str = "json",
        response_wire: Any = None,
        options: RequestOptions | None = None,
    ) -> Any:
        prepared = _core.prepare(
            self._settings, method, path, query=query, body=body, body_wire=body_wire, files=files, form=form,
            returns=returns, response_wire=response_wire, options=options,
        )  # fmt: skip
        attempt = 0
        while True:
            try:
                response = self._http.request(
                    prepared.method,
                    prepared.url,
                    headers=prepared.headers,
                    params=prepared.params,
                    content=prepared.content,
                    files=prepared.files,
                    data=prepared.data,
                    timeout=prepared.timeout,
                )
            except httpx.TransportError as exc:
                if attempt < prepared.max_retries and _core.transport_retriable(exc, prepared.idempotent):
                    self._sleep(_core.backoff_delay(attempt, None))
                    attempt += 1
                    continue
                raise _core.connection_error(exc) from exc
            if response.is_success:
                return _core.parse_success(response, prepared)
            error = _core.error_from_response(response)
            if attempt < prepared.max_retries and _core.status_retriable(
                response.status_code, prepared.idempotent, error.retry_after
            ):
                self._sleep(_core.backoff_delay(attempt, error.retry_after))
                attempt += 1
                continue
            raise error


class AsyncPayrudder(AsyncResources):
    """Asynchronous client; the same methods as :class:`Payrudder`, awaited. Paginators are async generators."""

    def __init__(
        self,
        api_key: str | None = None,
        *,
        base_url: str | None = None,
        timeout: float = _core.DEFAULT_TIMEOUT,
        max_retries: int = _core.DEFAULT_MAX_RETRIES,
        user_agent: str | None = None,
        default_headers: Mapping[str, str] | None = None,
        http_client: httpx.AsyncClient | None = None,
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        if http_client is not None and transport is not None:
            raise ValueError("pass http_client or transport, not both")
        self._settings = _core.resolve_settings(api_key, base_url, timeout, max_retries, user_agent, default_headers)
        self._owns_http = http_client is None
        self._http = http_client or httpx.AsyncClient(transport=transport)
        self._sleep: Callable[[float], Awaitable[None]] = asyncio.sleep
        self._init_resources(self)

    @property
    def base_url(self) -> str:
        return self._settings.base_url

    async def aclose(self) -> None:
        if self._owns_http:
            await self._http.aclose()

    async def __aenter__(self) -> AsyncPayrudder:
        return self

    async def __aexit__(
        self, exc_type: type[BaseException] | None, exc: BaseException | None, tb: TracebackType | None
    ) -> None:
        await self.aclose()

    async def _request(
        self,
        method: str,
        path: str,
        *,
        query: Mapping[str, Any] | None = None,
        body: Any = None,
        body_wire: Any = None,
        files: Any = None,
        form: Mapping[str, Any] | None = None,
        returns: str = "json",
        response_wire: Any = None,
        options: RequestOptions | None = None,
    ) -> Any:
        prepared = _core.prepare(
            self._settings, method, path, query=query, body=body, body_wire=body_wire, files=files, form=form,
            returns=returns, response_wire=response_wire, options=options,
        )  # fmt: skip
        attempt = 0
        while True:
            try:
                response = await self._http.request(
                    prepared.method,
                    prepared.url,
                    headers=prepared.headers,
                    params=prepared.params,
                    content=prepared.content,
                    files=prepared.files,
                    data=prepared.data,
                    timeout=prepared.timeout,
                )
            except httpx.TransportError as exc:
                if attempt < prepared.max_retries and _core.transport_retriable(exc, prepared.idempotent):
                    await self._sleep(_core.backoff_delay(attempt, None))
                    attempt += 1
                    continue
                raise _core.connection_error(exc) from exc
            if response.is_success:
                return _core.parse_success(response, prepared)
            error = _core.error_from_response(response)
            if attempt < prepared.max_retries and _core.status_retriable(
                response.status_code, prepared.idempotent, error.retry_after
            ):
                await self._sleep(_core.backoff_delay(attempt, error.retry_after))
                attempt += 1
                continue
            raise error

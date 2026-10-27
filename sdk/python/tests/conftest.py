"""A local fake of the gateway: a real HTTP server on 127.0.0.1 that answers from a script. No network, no stack."""

from __future__ import annotations

import json
import threading
from collections.abc import AsyncIterator, Callable, Iterator
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import parse_qsl, urlsplit

import pytest

from payrudder import AsyncPayrudder, Payrudder
from payrudder import models as m


@dataclass
class Recorded:
    method: str
    path: str
    query: dict[str, str]
    headers: dict[str, str]
    body: bytes

    def json(self) -> Any:
        return json.loads(self.body)


@dataclass
class Reply:
    status: int = 200
    body: Any = None  # dict/list -> JSON, str/bytes -> as is
    headers: dict[str, str] = field(default_factory=dict)
    drop: bool = False  # close the connection without answering


def new_customer(external_id: str) -> m.CreateCustomerRequest:
    return {"external_id": external_id, "name": "Test", "email": "t@example.com"}


def ok(body: Any = None, status: int = 200, **headers: str) -> Reply:
    return Reply(status, body if body is not None else {}, headers)


def err(status: int, code: str = "INVALID_ARGUMENT", key: str = "common.invalid_argument", **headers: str) -> Reply:
    envelope = {"error": {"code": code, "key": key, "message": f"boom {status}", "request_id": "req_1"}}
    return Reply(status, envelope, headers)


class FakeGateway:
    def __init__(self) -> None:
        self.requests: list[Recorded] = []
        self.script: list[Reply] = []
        self.handler: Callable[[Recorded], Reply] | None = None
        gateway = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *args: Any) -> None:
                pass

            def _serve(self) -> None:
                length = int(self.headers.get("Content-Length") or 0)
                body = self.rfile.read(length) if length else b""
                parts = urlsplit(self.path)
                rec = Recorded(
                    self.command,
                    parts.path,
                    dict(parse_qsl(parts.query)),
                    {k.lower(): v for k, v in self.headers.items()},
                    body,
                )
                gateway.requests.append(rec)
                if gateway.handler is not None:
                    reply = gateway.handler(rec)
                elif gateway.script:
                    reply = gateway.script.pop(0)
                else:
                    reply = Reply(599, {"error": {"code": "TEST", "key": "test.unscripted", "message": "no reply"}})
                if reply.drop:
                    self.close_connection = True
                    self.connection.close()
                    return
                if reply.status == 204:
                    payload, ctype = b"", "text/plain"
                elif isinstance(reply.body, (dict, list)):
                    payload = json.dumps(reply.body).encode()
                    ctype = "application/json"
                elif isinstance(reply.body, str):
                    payload, ctype = reply.body.encode(), "text/plain"
                else:
                    payload, ctype = reply.body or b"", "application/octet-stream"
                self.send_response(reply.status)
                headers = {"Content-Type": ctype, **reply.headers}
                for k, v in headers.items():
                    self.send_header(k, v)
                if reply.status != 204:
                    self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                if self.command != "HEAD":
                    self.wfile.write(payload)

            do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = _serve

        class QuickServer(ThreadingHTTPServer):
            def server_bind(self) -> None:
                # HTTPServer.server_bind calls socket.getfqdn(), which can take half a minute without DNS
                self.socket.bind(self.server_address)
                self.server_address = self.socket.getsockname()

        self.server = QuickServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}"
        self.thread = threading.Thread(target=self.server.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True)

    def start(self) -> None:
        self.thread.start()

    def stop(self) -> None:
        self.server.shutdown()
        self.server.server_close()


@pytest.fixture
def gateway() -> Iterator[FakeGateway]:
    gw = FakeGateway()
    gw.start()
    yield gw
    gw.stop()


class Sleeps:
    """Replaces the client's sleep so retries are instant and their delays can be asserted."""

    def __init__(self) -> None:
        self.delays: list[float] = []

    def sync(self, seconds: float) -> None:
        self.delays.append(seconds)

    async def asynchronous(self, seconds: float) -> None:
        self.delays.append(seconds)


@pytest.fixture
def sleeps() -> Sleeps:
    return Sleeps()


@pytest.fixture
def client(gateway: FakeGateway, sleeps: Sleeps) -> Iterator[Payrudder]:
    c = Payrudder("qk_test_abcd1234_secret", base_url=gateway.url)
    c._sleep = sleeps.sync
    yield c
    c.close()


@pytest.fixture
async def aclient(gateway: FakeGateway, sleeps: Sleeps) -> AsyncIterator[AsyncPayrudder]:
    c = AsyncPayrudder("qk_test_abcd1234_secret", base_url=gateway.url)
    c._sleep = sleeps.asynchronous
    yield c
    await c.aclose()

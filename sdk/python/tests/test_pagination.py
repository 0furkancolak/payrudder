from __future__ import annotations

import json

from payrudder import AsyncPayrudder, Payrudder

from .conftest import FakeGateway, Recorded, Reply


def pages(rec: Recorded) -> Reply:
    token = rec.query.get("page_token", "")
    data = {
        "": ({"customers": [{"external_id": "a"}, {"external_id": "b"}], "next_page_token": "t2"}),
        "t2": ({"customers": [{"external_id": "c"}], "next_page_token": "t3"}),
        "t3": ({"customers": [{"external_id": "d"}], "next_page_token": ""}),
    }[token]
    return Reply(200, data)


def test_iterator_walks_every_page(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.handler = pages
    ids = [c["external_id"] for c in client.customers.iter_customers(page_size=2)]
    assert ids == ["a", "b", "c", "d"]
    assert [r.query.get("page_token") for r in gateway.requests] == [None, "t2", "t3"]
    assert all(r.query["page_size"] == "2" for r in gateway.requests)


def test_iterator_is_lazy(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.handler = pages
    it = client.customers.iter_customers()
    assert gateway.requests == []
    assert next(it)["external_id"] == "a"
    assert len(gateway.requests) == 1


def test_raw_single_page_call_still_returns_the_token(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.handler = pages
    page = client.customers.list_customers()
    assert page["next_page_token"] == "t2"


def test_iterator_with_a_path_parameter_and_filters(client: Payrudder, gateway: FakeGateway) -> None:
    gateway.handler = lambda rec: Reply(200, {"entries": [{"id": "e1"}], "next_page_token": ""})
    entries = list(client.customers.iter_credit_ledger("cus_1"))
    assert entries == [{"id": "e1"}]
    assert gateway.requests[0].path == "/v1/customers/cus_1/credits/ledger"


async def test_async_iterator(aclient: AsyncPayrudder, gateway: FakeGateway) -> None:
    gateway.handler = pages
    ids = [c["external_id"] async for c in aclient.customers.iter_customers()]
    assert ids == ["a", "b", "c", "d"]
    assert json.dumps(ids)

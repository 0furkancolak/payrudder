"""Optional smoke test against a running stack: skipped unless PAYRUDDER_API_KEY is set (and, if it is not the
default, PAYRUDDER_BASE_URL). Use a qk_test_ key: the test writes to the sandbox."""

from __future__ import annotations

import os
import uuid

import pytest

from payrudder import NotFoundError, Payrudder

from .conftest import new_customer

pytestmark = pytest.mark.skipif(not os.environ.get("PAYRUDDER_API_KEY"), reason="PAYRUDDER_API_KEY is not set")


def test_create_and_read_a_customer() -> None:
    external_id = f"sdk-smoke-{uuid.uuid4().hex[:12]}"
    with Payrudder() as client:
        created = client.customers.create_customer(new_customer(external_id))
        assert created["external_id"] == external_id
        assert client.customers.get_customer(external_id)["id"] == created["id"]
        with pytest.raises(NotFoundError):
            client.customers.get_customer(external_id + "-missing")

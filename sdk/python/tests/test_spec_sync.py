"""The SDK cannot drift from api/openapi/v1.yaml: every /v1 operationId has a method, and nothing else does."""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path
from typing import Any

import pytest
import yaml

from payrudder import AsyncPayrudder, Payrudder
from payrudder._opids import OPERATIONS

ROOT = Path(__file__).resolve().parents[1]
SPEC = ROOT.parents[1] / "api" / "openapi" / "v1.yaml"

pytestmark = pytest.mark.skipif(not SPEC.exists(), reason="the repository's OpenAPI file is not next to the SDK")

# Helpers that are not operations: ingest batches, and iter_* walk every page of a list operation.
HELPERS = {"ingest"}


def snake(name: str) -> str:
    return re.sub(r"(?<=[a-z0-9])(?=[A-Z])|(?<=[A-Z])(?=[A-Z][a-z])", "_", name).lower()


def spec_operations() -> dict[str, tuple[str, str]]:
    spec = yaml.safe_load(SPEC.read_text())
    found: dict[str, tuple[str, str]] = {}
    for path, item in spec["paths"].items():
        if not path.startswith("/v1/"):
            continue
        for method in ("get", "post", "put", "patch", "delete"):
            if method in item:
                found[item[method]["operationId"]] = (path.split("/")[2].replace("-", "_"), f"{method.upper()} {path}")
    return found


def methods_of(client: Any) -> dict[str, set[str]]:
    out: dict[str, set[str]] = {}
    for name in dir(client):
        if name.startswith("_"):
            continue
        ns = getattr(client, name)
        if type(ns).__module__ == "payrudder.resources":
            out[name] = {m for m in dir(ns) if not m.startswith("_") and callable(getattr(ns, m))}
    return out


@pytest.mark.parametrize("cls", [Payrudder, AsyncPayrudder])
def test_every_operation_has_a_method_and_nothing_else_does(cls: Any) -> None:
    client = cls("qk_test_x_y")
    have = methods_of(client)
    expected = spec_operations()
    assert expected, "no /v1 operations found in the spec"

    wanted: dict[str, set[str]] = {}
    for op_id, (resource, _) in expected.items():
        wanted.setdefault(resource, set()).add(snake(op_id))

    missing = {r: sorted(m - have.get(r, set())) for r, m in wanted.items() if m - have.get(r, set())}
    assert not missing, f"operations without a client method: {missing}"

    for resource, names in have.items():
        extra = {n for n in names - wanted.get(resource, set()) if n not in HELPERS and not n.startswith("iter_")}
        assert not extra, f"{resource} has methods that are not an operationId of the spec: {sorted(extra)}"
    assert set(have) == set(wanted)


def test_generated_operation_table_matches_the_spec() -> None:
    expected = spec_operations()
    assert {k: (v[0], v[2]) for k, v in OPERATIONS.items()} == {k: (v[0], v[1]) for k, v in expected.items()}
    assert all(name == snake(op_id) for op_id, (_, name, _) in OPERATIONS.items())


def test_generated_files_are_up_to_date() -> None:
    result = subprocess.run(
        [sys.executable, str(ROOT / "tools" / "generate.py"), "--check"], capture_output=True, text=True, check=False
    )
    assert result.returncode == 0, result.stderr


def test_every_iterator_walks_a_paginated_list() -> None:
    spec = yaml.safe_load(SPEC.read_text())
    paged = set()
    for path, item in spec["paths"].items():
        if path.startswith("/v1/") and "get" in item:
            names = [p.get("name") for p in item["get"].get("parameters", []) if isinstance(p, dict)]
            if "page_token" in names:
                paged.add(snake(item["get"]["operationId"]))
    client = Payrudder("qk_test_x_y")
    iters = {n for names in methods_of(client).values() for n in names if n.startswith("iter_")}
    assert len(iters) == len(paged) > 10

"""Conversion between Python values and the JSON the API speaks.

* int64 values travel as JSON strings: they are ``int`` in Python. Which fields those are comes from the spec
  (``_wire_tables``); any key ending in ``_minor`` is also treated as int64, so data the spec leaves untyped (the
  ``data`` of a webhook event) behaves the same.
* ``Decimal`` is sent as its plain text, never as a float; a ``float`` in a request is refused.
* ``datetime`` / ``date`` are sent as ISO 8601 (naive datetimes are taken as UTC).
"""

from __future__ import annotations

import re
from datetime import date, datetime, timezone
from decimal import Decimal
from typing import Any

from ._wire_tables import MODELS

_INT = ("int",)
_DIGITS = re.compile(r"-?\d+")


def decode(value: Any, desc: Any = None) -> Any:
    """JSON -> Python (int64 strings become ints)."""
    return _walk(value, desc, False)


def encode(value: Any, desc: Any = None) -> Any:
    """Python -> JSON-ready values (ints of int64 fields become strings, Decimal/datetime become text)."""
    return _walk(value, desc, True)


def _walk(value: Any, desc: Any, enc: bool) -> Any:
    if isinstance(desc, str):
        desc = ("model", desc)
    if isinstance(value, dict):
        if desc is not None and desc[0] == "map":
            return {k: _walk(v, desc[1], enc) for k, v in value.items()}
        fields = MODELS.get(desc[1], {}) if desc is not None and desc[0] == "model" else {}
        return {k: _walk(v, fields.get(k) or (_INT if k.endswith("_minor") else None), enc) for k, v in value.items()}
    if isinstance(value, (list, tuple)):
        inner = desc[1] if desc is not None and desc[0] == "list" else None
        return [_walk(v, inner, enc) for v in value]
    if enc:
        return _scalar_out(value, desc)
    if desc == _INT and isinstance(value, str) and _DIGITS.fullmatch(value):
        return int(value)
    return value


def _scalar_out(value: Any, desc: Any) -> Any:
    if isinstance(value, bool) or value is None or isinstance(value, str):
        return value
    if isinstance(value, int):
        return str(value) if desc == _INT else value
    if isinstance(value, Decimal):
        return format(value, "f")
    if isinstance(value, datetime):
        if value.tzinfo is None:
            return value.isoformat() + "Z"
        return value.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")
    if isinstance(value, date):
        return value.isoformat()
    if isinstance(value, float):
        raise TypeError("floats are not accepted (they lose precision): pass a str, int or Decimal")
    return value

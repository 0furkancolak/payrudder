"""Helpers shared by the generated code."""

from __future__ import annotations

from urllib.parse import quote


def path_segment(value: str) -> str:
    """Percent-encode one path parameter; an empty one would silently address the collection."""
    text = str(value)
    if not text:
        raise ValueError("a path parameter must not be empty")
    return quote(text, safe="")

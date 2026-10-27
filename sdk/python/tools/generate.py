"""Generate the typed models, the wire tables and the resource classes from api/openapi/v1.yaml.

Run from sdk/python:  python tools/generate.py        (rewrites src/payrudder/{models,_wire_tables,resources}.py)
                      python tools/generate.py --check (exits 1 when the committed files are stale)

Scope: operations under /v1/* only (the tenant API). Names follow one mechanical rule: a method is the snake_case of
the operationId, grouped under the first path segment. Nothing here guesses: a schema shape the generator does not know is an
error, so the SDK cannot drift silently.
"""

from __future__ import annotations

import json
import keyword
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
SPEC = ROOT.parents[1] / "api" / "openapi" / "v1.yaml"
OUT = ROOT / "src" / "payrudder"

HTTP_METHODS = ("get", "post", "put", "patch", "delete")
# Names that must not be used as TypedDict field names because annotations in the same class body refer to them.
RESERVED_FIELDS = {
    "str",
    "int",
    "float",
    "bool",
    "list",
    "dict",
    "Any",
    "Literal",
    "date",
    "datetime",
    "Decimal",
    "None",
}

Desc = Any  # None | ("int",) | ("model", name) | ("list", desc) | ("map", desc)


def snake(name: str) -> str:
    return re.sub(r"(?<=[a-z0-9])(?=[A-Z])|(?<=[A-Z])(?=[A-Z][a-z])", "_", name).replace("-", "_").lower()


def pascal(name: str) -> str:
    parts = re.split(r"[^A-Za-z0-9]+", snake(name))
    return "".join(p[:1].upper() + p[1:] for p in parts if p)


def doc(text: str | None, limit: int = 400) -> str:
    if not text:
        return ""
    first = " ".join(text.strip().split("\n\n")[0].split())
    first = first.replace("\\", "\\\\").replace('"""', "'''")
    if len(first) > limit:
        first = first[: limit - 3].rstrip() + "..."
    if first.endswith('"'):
        first += " "
    return first


@dataclass
class TypeInfo:
    expr: str
    desc: Desc = None
    deps: set[str] = field(default_factory=set)


@dataclass
class Field_:
    wire: str
    expr: str
    required: bool
    desc: Desc


@dataclass
class Model:
    name: str
    fields: list[Field_] = field(default_factory=list)
    alias: str | None = None  # non-TypedDict: a plain alias expression
    description: str = ""
    deps: set[str] = field(default_factory=set)


@dataclass
class Param:
    wire: str
    py: str
    expr: str
    required: bool


@dataclass
class Op:
    op_id: str
    method: str
    path: str
    resource: str
    name: str
    summary: str
    path_params: list[Param]
    query: list[Param]
    body: TypeInfo | None
    body_required: bool
    multipart: bool
    response: TypeInfo | None
    kind: str  # json | text | bytes | none
    page_items: str | None = None  # items type expression when the op paginates
    page_key: str | None = None


class Gen:
    def __init__(self, spec: dict[str, Any]) -> None:
        self.spec = spec
        self.models: dict[str, Model] = {}
        self.order: list[str] = []
        self.ops: list[Op] = []
        self._in_progress: set[str] = set()

    # ---- refs ----
    def ref(self, node: Any) -> tuple[Any, str | None]:
        name = None
        while isinstance(node, dict) and "$ref" in node:
            target = node["$ref"]
            cur: Any = self.spec
            for part in target.lstrip("#/").split("/"):
                cur = cur[part]
            if target.startswith("#/components/schemas/"):
                name = target.rsplit("/", 1)[1]
            node = cur
        return node, name

    # ---- schema -> type ----
    def is_int(self, schema: dict[str, Any], prop: str | None) -> bool:
        if schema.get("format") == "int64":
            return True
        types = schema.get("type")
        types = [types] if isinstance(types, str) else (types or [])
        return bool(prop and prop.endswith("_minor") and ("string" in types or "integer" in types))

    def component(self, name: str) -> TypeInfo:
        if name not in self.models and name not in self._in_progress:
            self._in_progress.add(name)
            schema, _ = self.ref(self.spec["components"]["schemas"][name])
            self.define(name, schema)
            self._in_progress.discard(name)
        return TypeInfo(name, ("model", name), {name})

    def merged(self, schema: dict[str, Any]) -> dict[str, Any]:
        out: dict[str, Any] = {"type": "object", "properties": {}, "required": []}
        for part in schema["allOf"]:
            node, _ = self.ref(part)
            if "allOf" in node:
                node = self.merged(node)
            out["properties"].update(node.get("properties", {}))
            out["required"] += node.get("required", [])
        return out

    def define(self, name: str, schema: dict[str, Any]) -> None:
        """Register `name` for an object-ish schema (TypedDict or alias)."""
        description = doc(schema.get("description"))
        if "allOf" in schema:
            schema = {**self.merged(schema), "description": schema.get("description")}
        types = schema.get("type")
        if isinstance(types, list):
            types = next(t for t in types if t != "null")
        if types == "object" and schema.get("properties"):
            model = Model(name, description=description)
            required = set(schema.get("required", []))
            for prop, sub in schema["properties"].items():
                if prop in RESERVED_FIELDS:
                    raise SystemExit(f"field {name}.{prop} collides with a name used in annotations")
                info = self.type_of(sub, f"{name}{pascal(prop)}", prop)
                model.fields.append(Field_(prop, info.expr, prop in required, info.desc))
                model.deps |= info.deps
            self.models[name] = model
            self.order.append(name)
            return
        info = self.type_of(schema, name, None, inline_name=False)
        model = Model(name, alias=info.expr, description=description, deps=info.deps)
        model.fields = []
        self.models[name] = model
        self.order.append(name)
        model.alias_desc = info.desc  # type: ignore[attr-defined]

    def type_of(self, schema: Any, ctx: str, prop: str | None, inline_name: bool = True) -> TypeInfo:
        node, comp = self.ref(schema)
        if comp:
            return self.component(comp)
        schema = node
        nullable = bool(schema.get("nullable"))
        if "allOf" in schema:
            parts = schema["allOf"]
            if len(parts) == 1:
                info = self.type_of(parts[0], ctx, prop)
            else:
                self.define(ctx, schema)
                info = TypeInfo(ctx, ("model", ctx), {ctx})
            return self._nullable(info, nullable)
        if "oneOf" in schema or "anyOf" in schema:
            members = schema.get("oneOf") or schema.get("anyOf")
            infos: list[TypeInfo] = []
            has_null = False
            has_str = any((self.ref(m)[0].get("type") == "string") for m in members)
            for i, m in enumerate(members):
                mnode, _ = self.ref(m)
                if mnode.get("type") == "null":
                    has_null = True
                    continue
                if mnode.get("type") == "number" and has_str:
                    # a decimal that may travel as a JSON number: never a float on our side
                    infos.append(TypeInfo("int | Decimal"))
                    continue
                infos.append(self.type_of(m, f"{ctx}{i}", prop))
            expr = " | ".join(dict.fromkeys(i.expr for i in infos))
            desc = infos[0].desc if len(infos) == 1 else None
            deps: set[str] = set().union(*(i.deps for i in infos))
            return self._nullable(TypeInfo(expr, desc, deps), has_null or nullable)
        types = schema.get("type")
        if isinstance(types, list):
            nullable = nullable or "null" in types
            types = [t for t in types if t != "null"]
            types = types[0] if types else None
        if "enum" in schema:
            values = [v for v in schema["enum"] if v is not None]
            lit = ", ".join(json.dumps(v) if isinstance(v, str) else repr(v) for v in values)
            return self._nullable(TypeInfo(f"Literal[{lit}]"), nullable or None in schema["enum"])
        if types == "string":
            fmt = schema.get("format")
            if fmt == "date-time":
                return self._nullable(TypeInfo("@DT@"), nullable)
            if fmt == "date":
                return self._nullable(TypeInfo("@D@"), nullable)
            if self.is_int(schema, prop):
                return self._nullable(TypeInfo("int", ("int",)), nullable)
            return self._nullable(TypeInfo("str"), nullable)
        if types == "integer":
            return self._nullable(TypeInfo("int", ("int",) if self.is_int(schema, prop) else None), nullable)
        if types == "number":
            return self._nullable(TypeInfo("float"), nullable)
        if types == "boolean":
            return self._nullable(TypeInfo("bool"), nullable)
        if types == "array":
            item = self.type_of(schema.get("items", {}), f"{ctx}Item", None)
            desc = ("list", item.desc) if item.desc else None
            return self._nullable(TypeInfo(f"list[{item.expr}]", desc, item.deps), nullable)
        if types == "object" or (types is None and "properties" in schema):
            if schema.get("properties"):
                if not inline_name:
                    raise SystemExit(f"unnamed object at {ctx}")
                self.define(ctx, schema)
                return self._nullable(TypeInfo(ctx, ("model", ctx), {ctx}), nullable)
            ap = schema.get("additionalProperties")
            if isinstance(ap, dict) and ap:
                item = self.type_of(ap, f"{ctx}Value", None)
                desc = ("map", item.desc) if item.desc else None
                return self._nullable(TypeInfo(f"dict[str, {item.expr}]", desc, item.deps), nullable)
            return self._nullable(TypeInfo("dict[str, Any]"), nullable)
        if types is None:
            return TypeInfo("Any")
        raise SystemExit(f"unsupported schema at {ctx}: {schema}")

    @staticmethod
    def _nullable(info: TypeInfo, nullable: bool) -> TypeInfo:
        if nullable and not info.expr.endswith("| None") and info.expr != "Any":
            return TypeInfo(f"{info.expr} | None", info.desc, info.deps)
        return info

    # ---- operations ----
    def collect(self) -> None:
        names: set[str] = set()
        for path, item in self.spec["paths"].items():
            if not path.startswith("/v1/"):
                continue
            shared = item.get("parameters", [])
            for method in HTTP_METHODS:
                op = item.get(method)
                if op is None:
                    continue
                if op["operationId"] in names:
                    raise SystemExit(f"duplicate operationId {op['operationId']}")
                names.add(op["operationId"])
                self.ops.append(self.operation(path, method, op, shared))

    def operation(self, path: str, method: str, op: dict[str, Any], shared: list[Any]) -> Op:
        op_id = op["operationId"]
        base = pascal(op_id)
        path_params: list[Param] = []
        query: list[Param] = []
        for raw in shared + op.get("parameters", []):
            p, _ = self.ref(raw)
            if p["in"] == "header":
                continue
            py = p["name"] + "_" if keyword.iskeyword(p["name"]) else p["name"]
            schema, _ = self.ref(p["schema"])
            if p["in"] == "path":
                path_params.append(Param(p["name"], py, "str", True))
            else:
                if schema.get("type") in ("array", "object"):
                    raise SystemExit(f"{op_id}: structured query parameter {p['name']} is not supported")
                expr = self.type_of(schema, f"{base}{pascal(p['name'])}", None).expr
                expr = expr.replace("@DT@", "str | datetime").replace("@D@", "str | date")
                query.append(Param(p["name"], py, expr, bool(p.get("required"))))
        order = re.findall(r"\{([^}]+)\}", path)
        path_params.sort(key=lambda x: order.index(x.wire))

        body = None
        body_required = False
        multipart = False
        rb = op.get("requestBody")
        if rb:
            rb, _ = self.ref(rb)
            content = rb["content"]
            body_required = bool(rb.get("required"))
            if "application/json" in content:
                body = self.type_of(content["application/json"]["schema"], f"{base}Request", None)
            elif "multipart/form-data" in content:
                multipart = True
            else:
                raise SystemExit(f"{op_id}: unsupported request content {list(content)}")

        response = None
        kind = "none"
        for code in sorted(op["responses"]):
            if not code.startswith("2"):
                continue
            r, _ = self.ref(op["responses"][code])
            content = r.get("content") or {}
            if "application/json" in content:
                response = self.type_of(content["application/json"]["schema"], f"{base}Response", None)
                kind = "json"
            elif "text/csv" in content:
                kind = "text"
            elif "application/octet-stream" in content:
                kind = "bytes"
            if kind != "none":
                break

        o = Op(
            op_id,
            method.upper(),
            path,
            path.split("/")[2],
            snake(op_id),
            doc(op.get("summary")),
            path_params,
            query,
            body,
            body_required,
            multipart,
            response,
            kind,
        )
        o.resource = snake(o.resource)
        names = [p.py for p in path_params + query] + ["self", "body", "options", "files", "metadata"]
        if len(names) != len(set(names)) and not multipart:
            raise SystemExit(f"{op_id}: parameter name collision {names}")
        self.pagination(o, op)
        return o

    def pagination(self, o: Op, op: dict[str, Any]) -> None:
        if o.method != "GET" or not o.response or o.kind != "json":
            return
        if not any(q.wire == "page_token" for q in o.query):
            return
        model = self.models.get(o.response.expr)
        if model is None:
            return
        names = [f.wire for f in model.fields]
        if "next_page_token" not in names:
            raise SystemExit(f"{o.op_id}: takes page_token but the response has no next_page_token")
        arrays = [f for f in model.fields if f.expr.startswith("list[")]
        if not arrays:
            raise SystemExit(f"{o.op_id}: paginated response has no array")
        o.page_key = arrays[0].wire
        o.page_items = arrays[0].expr[len("list[") : -1]

    # ---- reach / wire tables ----
    def closure(self, roots: set[str]) -> set[str]:
        seen: set[str] = set()
        stack = list(roots)
        while stack:
            n = stack.pop()
            if n in seen or n not in self.models:
                continue
            seen.add(n)
            stack.extend(self.models[n].deps)
        return seen

    def wire_tables(self) -> dict[str, dict[str, Desc]]:
        def needs(desc: Desc, live: set[str]) -> bool:
            if desc is None:
                return False
            if desc[0] == "int":
                return True
            if desc[0] == "model":
                return desc[1] in live
            return needs(desc[1], live)

        def prune(desc: Desc, live: set[str]) -> Desc:
            if desc is None or not needs(desc, live):
                return None
            if desc[0] in ("list", "map"):
                return (desc[0], prune(desc[1], live))
            return desc

        live: set[str] = set()
        changed = True
        while changed:
            changed = False
            for name, m in self.models.items():
                if name in live:
                    continue
                if m.alias is not None:
                    d = getattr(m, "alias_desc", None)
                    if needs(d, live):
                        live.add(name)
                        changed = True
                elif any(needs(f.desc, live) for f in m.fields):
                    live.add(name)
                    changed = True
        out: dict[str, dict[str, Desc]] = {}
        for name in sorted(live):
            m = self.models[name]
            if m.alias is not None:
                out[name] = {"": prune(getattr(m, "alias_desc", None), live)}
            else:
                out[name] = {f.wire: prune(f.desc, live) for f in m.fields if needs(f.desc, live)}
        return out

    def wire_of(self, info: TypeInfo | None, live: dict[str, dict[str, Desc]]) -> str:
        if info is None or info.desc is None:
            return "None"

        def prune(d: Desc) -> Desc:
            if d is None:
                return None
            if d[0] == "int":
                return d
            if d[0] == "model":
                return d if d[1] in live else None
            inner = prune(d[1])
            return (d[0], inner) if inner else None

        d = prune(info.desc)
        if d is None:
            return "None"
        if d[0] == "model":
            return repr(d[1])
        return repr(d)


# ---- emit ----
HEADER = '''"""{what}

Generated by tools/generate.py from api/openapi/v1.yaml. Do not edit by hand.
"""
'''


def render_models(g: Gen, req_only: set[str]) -> str:
    out = [
        HEADER.format(what="Typed models of the Payrudder tenant API (TypedDict, field names as on the wire)."),
        "from __future__ import annotations\n",
        "from datetime import date, datetime",
        "from decimal import Decimal",
        "from typing import Any, Literal, TypedDict\n",
    ]
    emitted: set[str] = set()
    blocks: list[str] = []

    def emit(name: str, stack: tuple[str, ...] = ()) -> None:
        if name in emitted:
            return
        m = g.models[name]
        emitted.add(name)
        permissive = name in req_only
        sub = {"@DT@": "str | datetime" if permissive else "str", "@D@": "str | date" if permissive else "str"}

        def fix(expr: str) -> str:
            for k, v in sub.items():
                expr = expr.replace(k, v)
            return expr

        for dep in sorted(m.deps):
            if m.alias is not None or dep not in stack:
                emit(dep, (*stack, name))
        if m.alias is not None:
            blocks.append(f"{name} = {fix(m.alias)}")
            return
        bad = [f for f in m.fields if keyword.iskeyword(f.wire) or not f.wire.isidentifier()]
        docline = [f'    """{m.description}"""'] if m.description else []
        if bad:
            body = ", ".join(f'"{f.wire}": "{fix(f.expr).replace(chr(34), chr(39))}"' for f in m.fields)
            blocks.append(
                f"# {name}: a field is a Python keyword, so the functional form is used and every key is optional.\n"
                f'{name} = TypedDict("{name}", {{{body}}}, total=False)'
            )
            return
        req = [f for f in m.fields if f.required]
        opt = [f for f in m.fields if not f.required]
        if req and opt:
            blocks.append(
                "\n".join([f"class _{name}Required(TypedDict):"] + [f"    {f.wire}: {fix(f.expr)}" for f in req])
            )
            blocks.append(
                "\n".join(
                    [f"class {name}(_{name}Required, total=False):"]
                    + docline
                    + [f"    {f.wire}: {fix(f.expr)}" for f in opt]
                )
            )
        else:
            tail = "" if req else ", total=False"
            fields = [f"    {f.wire}: {fix(f.expr)}" for f in m.fields] or ([] if docline else ["    pass"])
            blocks.append("\n".join([f"class {name}(TypedDict{tail}):"] + docline + fields))

    for n in g.order:
        emit(n)
    return "\n".join(out) + "\n\n" + "\n\n\n".join(blocks) + "\n"


def render_wire(tables: dict[str, dict[str, Desc]], int_models: dict[str, dict[str, Desc]]) -> str:
    out = [
        HEADER.format(what="Which fields of which model are int64 (strings on the wire, ints in Python)."),
        "from __future__ import annotations\n",
        "from typing import Any\n",
        "MODELS: dict[str, dict[str, Any]] = {",
    ]
    for name, fields in tables.items():
        out.append(f"    {name!r}: {{")
        for k, v in fields.items():
            out.append(f"        {k!r}: {v!r},")
        out.append("    },")
    out.append("}")
    return "\n".join(out) + "\n"


def py_default(p: Param) -> str:
    return f"{p.py}: {p.expr}" if p.required else f"{p.py}: {p.expr} | None = None"


def fix_expr(expr: str, permissive: bool) -> str:
    return expr.replace("@DT@", "str | datetime" if permissive else "str").replace(
        "@D@", "str | date" if permissive else "str"
    )


def M(expr: str) -> str:
    """Qualify the model names in a type expression with the `m.` module alias (string literals stay as they are)."""
    keep = ("Any", "Literal", "Decimal", "None")
    parts = re.split(r'("[^"]*")', expr)
    return "".join(
        part
        if part.startswith('"')
        else re.sub(
            r"\b([A-Z][A-Za-z0-9]*)\b", lambda mo: mo.group(1) if mo.group(1) in keep else f"m.{mo.group(1)}", part
        )
        for part in parts
    )


def resp_type(g: Gen, o: Op, req_only: set[str]) -> str:
    if o.kind == "text":
        return "str"
    if o.kind == "bytes":
        return "bytes"
    if o.kind == "none" or o.response is None:
        return "None"
    return M(fix_expr(o.response.expr, False))


def render_op(g: Gen, o: Op, live: dict[str, dict[str, Desc]], req_only: set[str], is_async: bool) -> list[str]:
    aw = "await " if is_async else ""
    df = "async def" if is_async else "def"
    ret = resp_type(g, o, req_only)
    sig = ["self"]
    sig += [f"{p.py}: str" for p in o.path_params]
    if o.multipart:
        sig += ["files: Sequence[FileUpload]", "*", "metadata: Mapping[str, Any] | None = None"]
    else:
        if o.body is not None:
            bt = M(fix_expr(o.body.expr, True))
            sig.append(f"body: {bt}" if o.body_required else f"body: {bt} | None = None")
        sig.append("*")
        req_q = [p for p in o.query if p.required]
        opt_q = [p for p in o.query if not p.required]
        sig += [f"{p.py}: {M(p.expr)}" for p in req_q]
        sig += [f"{p.py}: {M(p.expr)} | None = None" for p in opt_q]
    sig.append("options: RequestOptions | None = None")
    path = o.path
    for p in o.path_params:
        path = path.replace("{" + p.wire + "}", "{_p(" + p.py + ")}")
    path_expr = f'f"{path}"' if o.path_params else f'"{path}"'
    args = [f'"{o.method}"', path_expr]
    if o.query:
        args.append("query={" + ", ".join(f'"{p.wire}": {p.py}' for p in o.query) + "}")
    if o.body is not None:
        args.append("body=body")
        w = g.wire_of(o.body, live)
        if w != "None":
            args.append(f"body_wire={w}")
    if o.multipart:
        args.append("files=files")
        args.append("form=metadata")
    wr = g.wire_of(o.response, live) if o.kind == "json" else "None"
    if wr != "None":
        args.append(f"response_wire={wr}")
    if o.kind != "json":
        args.append(f'returns="{o.kind}"')
    args.append("options=options")
    call = f"self._client._request({', '.join(args)})"
    lines = [
        f"    {df} {o.name}({', '.join(sig)}) -> {ret}:",
        f'        """{o.summary}.\n\n        ``{o.method} {o.path}``\n        """',
    ]
    if ret == "None":
        lines.append(f"        {aw}{call}")
    else:
        lines.append(f'        return cast("{ret}", {aw}{call})')
    lines.append("")
    if o.page_key:
        lines += render_iter(o, is_async)
    if o.op_id == "ingestEvents":
        lines += render_ingest(is_async)
    return lines


def render_iter(o: Op, is_async: bool) -> list[str]:
    name = "iter_" + (o.name[5:] if o.name.startswith("list_") else o.name)
    qs = [p for p in o.query if p.wire != "page_token"]
    sig = ["self"] + [f"{p.py}: str" for p in o.path_params] + ["*"]
    sig += [f"{p.py}: {M(p.expr)}" for p in qs if p.required]
    sig += [f"{p.py}: {M(p.expr)} | None = None" for p in qs if not p.required]
    sig.append("options: RequestOptions | None = None")
    item = M(fix_expr(o.page_items or "Any", False))
    fwd = ", ".join(f"{p.py}" for p in o.path_params) + (", " if o.path_params else "")
    kw = ", ".join(f"{p.py}={p.py}" for p in qs)
    kw += (", " if kw else "") + "options=options"
    key = o.page_key
    if is_async:
        return [
            f"    async def {name}({', '.join(sig)}) -> AsyncIterator[{item}]:",
            f'        """Every item of ``{o.name}``, following ``next_page_token`` until the last page."""',
            "        token: str | None = None",
            "        while True:",
            f"            page = await self.{o.name}({fwd}{kw}, page_token=token)",
            f'            for item in page.get("{key}", []):',
            "                yield item",
            '            token = page.get("next_page_token") or None',
            "            if token is None:",
            "                return",
            "",
        ]
    return [
        f"    def {name}({', '.join(sig)}) -> Iterator[{item}]:",
        f'        """Every item of ``{o.name}``, following ``next_page_token`` until the last page."""',
        "        token: str | None = None",
        "        while True:",
        f"            page = self.{o.name}({fwd}{kw}, page_token=token)",
        f'            yield from page.get("{key}", [])',
        '            token = page.get("next_page_token") or None',
        "            if token is None:",
        "                return",
        "",
    ]


def render_ingest(is_async: bool) -> list[str]:
    aw = "await " if is_async else ""
    df = "async def" if is_async else "def"
    return [
        f"    {df} ingest(self, events: Sequence[m.IncomingEvent], *, options: RequestOptions | None = None) -> list[m.EventResult]:",
        '        """Send any number of events: split into valid batches (1..500 events, under 1 MiB), fill a missing',
        "        ``idempotency_key`` once (so automatic retries of a batch are safe), and return one result per event in",
        '        input order. A rejected event is a result with ``status == "rejected"``, not an exception; a failed batch',
        '        raises the usual error."""',
        "        results: list[m.EventResult] = []",
        "        for batch in prepare_ingest_batches(events):",
        f'            response = {aw}self.ingest_events({{"events": batch}}, options=options)',
        '            results.extend(response.get("results", []))',
        "        return results",
        "",
    ]


def render_resources(g: Gen, live: dict[str, dict[str, Desc]], req_only: set[str]) -> str:
    groups: dict[str, list[Op]] = {}
    for o in g.ops:
        groups.setdefault(o.resource, []).append(o)
    out = [
        HEADER.format(what="Resource classes: one method per operationId of the tenant API (sync and async)."),
        "from __future__ import annotations\n",
        "from collections.abc import AsyncIterator, Iterator, Mapping, Sequence",
        "from datetime import date, datetime",
        "from decimal import Decimal",
        "from typing import TYPE_CHECKING, Any, Literal, cast\n",
        "from . import models as m",
        "from ._ingest import prepare_ingest_batches",
        "from ._util import path_segment as _p",
        "from .types import FileUpload, RequestOptions\n",
        "if TYPE_CHECKING:",
        "    from ._client import AsyncPayrudder, Payrudder\n",
    ]
    for is_async in (False, True):
        prefix = "Async" if is_async else ""
        client = "AsyncPayrudder" if is_async else "Payrudder"
        for res, ops in groups.items():
            cls = f"{prefix}{pascal(res)}Resource"
            out.append(f"\nclass {cls}:")
            out.append(f'    """``/v1/{res.replace("_", "-")}`` and related operations."""\n')
            out.append(f"    def __init__(self, client: {client}) -> None:")
            out.append("        self._client = client\n")
            for o in ops:
                out += render_op(g, o, live, req_only, is_async)
        out.append(f"\nclass {prefix}Resources:")
        out.append('    """The resource namespaces of a client."""\n')
        for res in groups:
            out.append(f"    {res}: {prefix}{pascal(res)}Resource")
        out.append(f"\n    def _init_resources(self, client: {client}) -> None:")
        for res in groups:
            out.append(f"        self.{res} = {prefix}{pascal(res)}Resource(client)")
        out.append("")
    text = "\n".join(out)
    return re.sub(r"\n{4,}", "\n\n\n", text).rstrip("\n") + "\n"


def build() -> dict[str, str]:
    spec = yaml.safe_load(SPEC.read_text())
    g = Gen(spec)
    g.collect()
    # the event envelope used by the webhook helpers has no schema in the spec
    req_roots: set[str] = set()
    resp_roots: set[str] = set()
    for o in g.ops:
        if o.body:
            req_roots |= o.body.deps
        if o.response:
            resp_roots |= o.response.deps
    req_only = g.closure(req_roots) - g.closure(resp_roots)
    live = g.wire_tables()
    return {
        "models.py": render_models(g, req_only),
        "_wire_tables.py": render_wire(live, live),
        "resources.py": render_resources(g, live, req_only),
        "_opids.py": render_opids(g),
    }


def render_opids(g: Gen) -> str:
    lines = [
        HEADER.format(what="Every /v1 operationId of the spec with its resource and method (used by the drift test)."),
        "from __future__ import annotations\n",
        "OPERATIONS: dict[str, tuple[str, str, str]] = {",
    ]
    for o in g.ops:
        lines.append(f'    "{o.op_id}": ("{o.resource}", "{o.name}", "{o.method} {o.path}"),')
    lines.append("}")
    return "\n".join(lines) + "\n"


def main() -> int:
    files = build()
    check = "--check" in sys.argv
    stale = []
    for name, text in files.items():
        target = OUT / name
        if check:
            if not target.exists() or target.read_text() != text:
                stale.append(name)
        else:
            target.write_text(text)
    if stale:
        print("stale generated files: " + ", ".join(stale) + " (run python tools/generate.py)", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

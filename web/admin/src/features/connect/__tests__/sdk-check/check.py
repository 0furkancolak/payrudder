"""Looks the calls of a Connect snippet up in the real Python SDK. usage: check.py snippet.py...  (PYTHONPATH has sdk/python/src)

The imports of the snippet are executed (a name that is not in the package is an ImportError) and every `client.<resource>.<method>(...)` is looked up on a client.
"""
import ast
import sys

from payrudder import Payrudder

client = Payrudder(api_key="qk_test_x", base_url="http://localhost")
for path in sys.argv[1:]:
    tree = ast.parse(open(path).read(), path)
    imports = [n for n in tree.body if isinstance(n, (ast.Import, ast.ImportFrom))]
    exec(compile(ast.Module(body=imports, type_ignores=[]), path, "exec"), {})
    for node in ast.walk(tree):
        f = node.func if isinstance(node, ast.Call) else None
        if isinstance(f, ast.Attribute) and isinstance(f.value, ast.Attribute) and isinstance(f.value.value, ast.Name) and f.value.value.id == "client":
            assert callable(getattr(getattr(client, f.value.attr), f.attr)), f"client.{f.value.attr}.{f.attr}"
print("ok")

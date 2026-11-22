"""
Inside an async def, the cache is reached only as `await cache.aio.<method>()`.
A plain cache call there blocks the event loop on Redis, and handing a cache
method to asyncio.to_thread (or similar) is a second convention that skips the
cache's own threads. Sync functions nested in an async def run elsewhere (a
background task, a thread) and are not checked.
"""

import ast
from pathlib import Path

PACKAGE = Path(__file__).resolve().parent.parent / "notifications_server"
OFFLOADERS = {"to_thread", "run_in_threadpool", "run_in_executor"}


def _cache_methods():
    tree = ast.parse((PACKAGE / "services" / "cache.py").read_text())
    cache_class = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == "Cache")
    return {
        n.name
        for n in cache_class.body
        if isinstance(n, ast.FunctionDef) and not n.name.startswith("_") and n.name != "aio"
    }


def _async_body_nodes(fn):
    """Every node in fn's body, without descending into nested functions."""
    stack = list(fn.body)
    while stack:
        node = stack.pop()
        yield node
        for child in ast.iter_child_nodes(node):
            if not isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)):
                stack.append(child)


def _violations():
    methods = _cache_methods()
    found = []
    for path in sorted(PACKAGE.rglob("*.py")):
        tree = ast.parse(path.read_text())
        awaited = {id(n.value) for n in ast.walk(tree) if isinstance(n, ast.Await)}
        for fn in ast.walk(tree):
            if not isinstance(fn, ast.AsyncFunctionDef):
                continue
            for node in _async_body_nodes(fn):
                if not isinstance(node, ast.Call):
                    continue
                func = node.func
                name = func.attr if isinstance(func, ast.Attribute) else getattr(func, "id", None)
                where = f"{path.relative_to(PACKAGE.parent)}:{node.lineno} in {fn.name}"
                if name in methods and id(node) not in awaited:
                    found.append(f"{where}: {ast.unparse(node)[:80]}")
                elif name in OFFLOADERS:
                    for arg in node.args:
                        if isinstance(arg, ast.Attribute) and arg.attr in methods:
                            found.append(f"{where}: {ast.unparse(node)[:80]}")
    return found


def test_async_code_reaches_the_cache_only_through_aio():
    assert _violations() == []


def test_the_check_sees_the_cache_methods():
    assert {"get_event_entry", "update_event_entry", "claim_card_action", "get_installations"} <= _cache_methods()

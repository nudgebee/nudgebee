"""Messages name the product from settings.urls.branding_name, never a literal:
a white-labelled deployment shows its own name everywhere."""

import ast
import re
from pathlib import Path

PACKAGE = Path(__file__).resolve().parent.parent / "notifications_server"
BRAND = re.compile(r"\bNudge ?[Bb]ee\b")
LOG_METHODS = {"debug", "info", "warning", "error", "exception", "critical", "log"}
# Literals no user reads: the default the branding falls back to, and an HTTP header.
ALLOWED = {
    ("configs/settings.py", "Nudgebee"),
    ("clients/discord_client.py", "NudgeBee (https://nudgebee.com, 1.0.0)"),
}


def _skipped_strings(tree):
    """Docstrings and log-call arguments: text that no user reads."""
    skipped = set()
    for node in ast.walk(tree):
        if isinstance(node, (ast.Module, ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)):
            first = node.body[0] if node.body else None
            if isinstance(first, ast.Expr) and isinstance(first.value, ast.Constant):
                skipped.add(id(first.value))
        elif isinstance(node, ast.Call) and getattr(node.func, "attr", None) in LOG_METHODS:
            for arg in node.args:
                skipped.update(id(n) for n in ast.walk(arg))
    return skipped


def test_python_strings_do_not_name_the_product():
    found = []
    for path in sorted(PACKAGE.rglob("*.py")):
        relative = path.relative_to(PACKAGE).as_posix()
        tree = ast.parse(path.read_text(), filename=relative)
        skipped = _skipped_strings(tree)
        for node in ast.walk(tree):
            if (
                isinstance(node, ast.Constant)
                and isinstance(node.value, str)
                and id(node) not in skipped
                and (relative, node.value) not in ALLOWED
                and BRAND.search(node.value)
            ):
                found.append(f"{relative}:{node.lineno}: {node.value[:80]!r}")
    assert not found, "use settings.urls.branding_name instead:\n" + "\n".join(found)


def test_email_templates_do_not_name_the_product():
    found = []
    for path in sorted((PACKAGE / "emailer" / "templates").glob("*.html")):
        for number, line in enumerate(path.read_text().splitlines(), 1):
            if BRAND.search(line.replace('default("Nudgebee", true)', "")):
                found.append(f"{path.name}:{number}: {line.strip()[:80]}")
    assert not found, "use {{ _brand_name }} instead:\n" + "\n".join(found)

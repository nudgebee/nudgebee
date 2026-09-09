"""event_rules ON CONFLICT target ↔ migration drift gate.

Postgres infers the arbiter index for ON CONFLICT (a, b, c) by *exact* column
match. When migration V873 re-keyed event_rules from
(account_id, tenant_id, alert) to (account_id, tenant_id, source, alert), the
api-server Go upsert was updated but the two collector writers were not. Every
batch they sent then raised 42P10 at plan time — before any conflict exists — so
the whole INSERT failed and Prometheus/cloud alert-rule ingestion stopped dead
for every account, with no symptom other than an empty `expr` column.

This test pins all writers to the unique index actually declared in the latest
migration that re-keys event_rules, so the next re-key cannot land without
sweeping the call sites. It reads the other services' source as text on purpose:
the drift is cross-language, so a Python-only assertion would not have caught it.

Runs offline — no DB, no imports of app code.
"""

import re
import unittest
from pathlib import Path

# app/tests/ -> app/ -> k8s-collector/ -> collector-server/ -> repo root
REPO_ROOT = Path(__file__).resolve().parents[4]
MIGRATIONS_DIR = REPO_ROOT / "api-server" / "migrations" / "migrations" / "app"

PY_HANDLER = REPO_ROOT / "collector-server" / "k8s-collector" / "app" / "handlers" / "alert_rules_handler.py"
GO_CLOUD_COLLECTOR = REPO_ROOT / "collector-server" / "cloud-collector" / "account" / "etl_events.go"
GO_API_SERVER = REPO_ROOT / "api-server" / "services" / "eventrule" / "service.go"

# CREATE UNIQUE INDEX [IF NOT EXISTS] <name> ON event_rules (col, col, ...)
_INDEX_RE = re.compile(
    r"CREATE\s+UNIQUE\s+INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?\w+\s+ON\s+event_rules\s*\(([^)]*)\)",
    re.IGNORECASE,
)
# ON CONFLICT (col, col, ...) — tolerant of case and of spaces around commas.
_CONFLICT_RE = re.compile(r"ON\s+CONFLICT\s*\(([^)]*)\)", re.IGNORECASE)
# The Python writer builds its clause from a tuple, so match the tuple instead.
_PY_TUPLE_RE = re.compile(r"EVENT_RULE_CONFLICT_COLUMNS\s*=\s*\(([^)]*)\)")


def _columns(raw: str) -> tuple:
    return tuple(c.strip().strip('"').strip("'") for c in raw.split(",") if c.strip())


def _conflict_targets(path: Path) -> list:
    """Every event_rules ON CONFLICT column tuple a writer declares."""
    text = path.read_text()
    if path.suffix == ".py":
        return [_columns(m.group(1)) for m in _PY_TUPLE_RE.finditer(text)]
    return [
        _columns(m.group(1))
        for m in _CONFLICT_RE.finditer(text)
        # Other tables are upserted in the same file; keep only the event_rules
        # key, identified by its leading account columns.
        if _columns(m.group(1))[:2] == ("account_id", "tenant_id")
    ]


def _expected_columns() -> tuple:
    """Columns of the event_rules unique index from the newest migration that
    declares one. Migration filenames are `{unix_ms}_V{N}_...` so a lexical max
    on the timestamp prefix is chronological."""
    found = []
    for path in MIGRATIONS_DIR.glob("*.up.sql"):
        match = _INDEX_RE.search(path.read_text())
        if match:
            found.append((path.name.split("_")[0], _columns(match.group(1))))
    if not found:
        raise AssertionError(f"no CREATE UNIQUE INDEX ON event_rules found under {MIGRATIONS_DIR}")
    return max(found, key=lambda pair: int(pair[0]))[1]


class EventRulesConflictTargetTest(unittest.TestCase):
    @unittest.skipUnless(MIGRATIONS_DIR.is_dir(), "migrations tree not present in this checkout")
    def test_every_writer_targets_the_current_unique_index(self):
        expected = _expected_columns()
        self.assertIn("source", expected, "V873 added `source` to the event_rules unique key")

        for path in (PY_HANDLER, GO_CLOUD_COLLECTOR, GO_API_SERVER):
            with self.subTest(writer=str(path.relative_to(REPO_ROOT))):
                targets = _conflict_targets(path)
                self.assertTrue(targets, f"no event_rules ON CONFLICT target found in {path.name}")
                for target in targets:
                    self.assertEqual(
                        target,
                        expected,
                        f"{path.name} upserts event_rules with ON CONFLICT {target}, but the "
                        f"unique index is {expected} — Postgres will raise 42P10 and drop the "
                        f"whole batch.",
                    )


if __name__ == "__main__":
    unittest.main()

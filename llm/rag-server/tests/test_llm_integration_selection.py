"""Which LLM credential an account resolves against.

Mirrors llm-server's chooseAccountLLMIntegration, so the two services never
disagree about which provider an account is using. Both run the shared cases in
llm/llm-server/agents/core/testdata/llm_default_cases.json.

Rows are (integration id, linked to the account, marked default on the account's
link, tenant default).
"""

import json
from pathlib import Path

import pytest

from rag.core.utils import db_query

CASES = Path(__file__).resolve().parents[2] / "llm-server/agents/core/testdata/llm_default_cases.json"


class FakeConnection:
    def __init__(self, rows):
        self.rows = rows

    def execute(self, *args, **kwargs):
        return self

    def fetchall(self):
        return self.rows

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


def use_rows(monkeypatch, rows):
    monkeypatch.setattr(db_query, "engine", type("E", (), {"connect": lambda self: FakeConnection(rows)})())


def candidates_for(account, configs):
    """What the selection query loads for one account: enabled configs linked to it, and the tenant default."""
    rows = []
    for c in configs:
        if c.get("enabled", True) is False:
            continue
        linked = account in c.get("accounts", [])
        tenant_default = c.get("tenant_default", False)
        if linked or tenant_default:
            rows.append((c["id"], linked, account in c.get("account_defaults", []), tenant_default))
    return rows


@pytest.mark.parametrize("case", json.loads(CASES.read_text(encoding="utf-8"))["cases"], ids=lambda c: c["name"])
def test_shared_cases(case):
    for account, want in case["accounts"].items():
        got = db_query.choose_account_llm_integration(account, candidates_for(account, case["configs"]))
        assert (got or "") == want["uses"], account


def test_account_default_wins(monkeypatch):
    use_rows(monkeypatch, [("a", True, False, False), ("b", True, True, False)])

    assert db_query.get_default_llm_integration("acct") == "b"


def test_an_only_config_that_is_not_marked_is_not_used(monkeypatch):
    # Picking never depends on how many configs an account has.
    use_rows(monkeypatch, [("only", True, False, False)])

    assert db_query.get_default_llm_integration("acct") is None


def test_unlinked_tenant_default_is_used(monkeypatch):
    use_rows(monkeypatch, [("a", True, False, False), ("t", False, False, True)])

    assert db_query.get_default_llm_integration("acct") == "t"


def test_account_default_outranks_the_tenant_default(monkeypatch):
    use_rows(monkeypatch, [("a", True, True, False), ("b", True, False, True)])

    assert db_query.get_default_llm_integration("acct") == "a"


def test_several_tenant_defaults_fall_through(monkeypatch):
    use_rows(monkeypatch, [("a", False, False, True), ("b", False, False, True)])

    assert db_query.get_default_llm_integration("acct") is None


def test_no_integrations(monkeypatch):
    use_rows(monkeypatch, [])

    assert db_query.get_default_llm_integration("acct") is None


def test_query_failure_is_not_fatal(monkeypatch):
    class Broken:
        def connect(self):
            raise RuntimeError("db down")

    monkeypatch.setattr(db_query, "engine", Broken())

    assert db_query.get_default_llm_integration("acct") is None

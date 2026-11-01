"""
A rule covers every account (scope_mode "all") or the accounts in its
notification_rule_accounts rows (scope_mode "accounts"). account_id is kept only
as a mirror of the first account for older clients and is never read by the
matcher, except on a database that predates scope_mode.
"""

import asyncio
import uuid
from types import SimpleNamespace

import pytest
from sqlalchemy.exc import ProgrammingError
from sqlalchemy.sql.elements import TextClause

from notifications_server.exceptions.notification_errors import InvalidRuleError
from notifications_server.models.models import NotificationRuleAccounts, NotificationRules
from notifications_server.services import rules as rules_module
from notifications_server.services.message import NotificationRuleMatcher, _scope_matches
from notifications_server.services.rules import (
    AUTOMATION_SOURCE,
    SCOPE_ACCOUNTS,
    SCOPE_ALL,
    NotificationRulesService,
    _resolve_scope,
)

TENANT = "22222222-2222-2222-2222-222222222222"
ACCT_A = "aaaaaaaa-0000-0000-0000-000000000001"
ACCT_B = "aaaaaaaa-0000-0000-0000-000000000002"
ACCT_C = "aaaaaaaa-0000-0000-0000-000000000003"
ALL_COLUMNS = [c.name for c in NotificationRules.__table__.columns]


def _rule(**overrides):
    base = dict(
        id="rule-1",
        name="rule-1",
        source="troubleshoot",
        account_id=None,
        scope_mode=SCOPE_ALL,
        account_ids=[],
        missing_columns=frozenset(),
        namespace=None,
        workload=None,
        aggregation_key=None,
        severity=None,
        is_active=True,
        is_suppressed=True,
        expires_at=None,
        delivery_mode="real_time",
        event_types=None,
    )
    base.update(overrides)
    return SimpleNamespace(**base)


# ----------------------------- matching -----------------------------


class _RulesService:
    def __init__(self, rules):
        self._rules = rules

    async def get_notification_rules(self, _tenant_id):
        return self._rules


def _match_finding(rules, account_id):
    payload = {"source": "troubleshoot", "parameters": {"finding": {"cloud_account_id": account_id}}}
    matcher = NotificationRuleMatcher(_RulesService(rules))
    matched, _ = asyncio.run(matcher.match_notification_rules(_Session(), TENANT, "finding", payload))
    return [m["rule_id"] for m in matched]


def test_rule_over_several_accounts_matches_each_of_them_only():
    rule = _rule(scope_mode=SCOPE_ACCOUNTS, account_id=ACCT_A, account_ids=[ACCT_A, ACCT_B])

    assert _match_finding([rule], ACCT_A) == ["rule-1"]
    assert _match_finding([rule], ACCT_B) == ["rule-1"]
    assert _match_finding([rule], ACCT_C) == []


def test_account_ids_decide_not_the_mirror():
    # An older client moved the mirror; the rule still follows its account rows.
    rule = _rule(scope_mode=SCOPE_ACCOUNTS, account_id=ACCT_C, account_ids=[ACCT_A])

    assert _match_finding([rule], ACCT_A) == ["rule-1"]
    assert _match_finding([rule], ACCT_C) == []


def test_all_accounts_rule_matches_any_account():
    assert _match_finding([_rule()], ACCT_C) == ["rule-1"]


@pytest.mark.parametrize("account_ids", [[], None])
def test_rule_with_no_loaded_accounts_matches_nothing(account_ids):
    # [] is a rule with no account rows; None is a failed account load.
    rule = _rule(scope_mode=SCOPE_ACCOUNTS, account_id=ACCT_A, account_ids=account_ids)

    assert _match_finding([rule], ACCT_A) == []


def test_unknown_scope_matches_nothing():
    assert not _scope_matches(_rule(scope_mode=None), ACCT_A)
    assert not _scope_matches(_rule(scope_mode="tenant"), ACCT_A)


def test_message_without_an_account_matches_only_all_accounts_rules():
    assert _scope_matches(_rule(), None)
    assert not _scope_matches(_rule(scope_mode=SCOPE_ACCOUNTS, account_ids=[ACCT_A]), None)


def test_database_before_scope_mode_matches_on_account_id():
    missing = frozenset({"scope_mode"})
    single = _rule(scope_mode=None, account_ids=None, account_id=ACCT_A, missing_columns=missing)
    everywhere = _rule(scope_mode=None, account_ids=None, account_id=None, missing_columns=missing)

    assert _scope_matches(single, ACCT_A)
    assert not _scope_matches(single, ACCT_B)
    assert _scope_matches(everywhere, ACCT_B)


def test_cached_rule_keeps_its_accounts():
    rule = NotificationRules(id=uuid.uuid4(), tenant_id=TENANT, name="r", source="troubleshoot")
    rule.scope_mode = SCOPE_ACCOUNTS
    rule.account_ids = [ACCT_A, ACCT_B]

    restored = NotificationRules.from_dict(rule.to_dict())

    assert restored.scope_mode == SCOPE_ACCOUNTS
    assert _scope_matches(restored, ACCT_B)
    assert not _scope_matches(restored, ACCT_C)


# ----------------------------- resolving a save -----------------------------


def _existing(scope_mode):
    return SimpleNamespace(scope_mode=scope_mode)


def test_older_client_account_id_scopes_to_that_account():
    assert _resolve_scope({"account_id": ACCT_A}, ["troubleshoot"], None, []) == (SCOPE_ACCOUNTS, [ACCT_A])


def test_account_ids_are_deduplicated_in_order():
    data = {"account_ids": [ACCT_B, ACCT_A, ACCT_B, None]}
    assert _resolve_scope(data, ["troubleshoot"], None, []) == (SCOPE_ACCOUNTS, [ACCT_B, ACCT_A])


def test_account_ids_win_over_the_mirror():
    data = {"account_id": ACCT_C, "account_ids": [ACCT_A, ACCT_B]}
    assert _resolve_scope(data, ["troubleshoot"], None, []) == (SCOPE_ACCOUNTS, [ACCT_A, ACCT_B])


def test_tenant_wide_sources_always_cover_every_account():
    data = {"account_ids": [ACCT_A], "scope_mode": SCOPE_ACCOUNTS}
    assert _resolve_scope(data, ["daily_recap"], None, []) == (SCOPE_ALL, [])


def test_older_client_cannot_collapse_a_multi_account_rule():
    with pytest.raises(InvalidRuleError):
        _resolve_scope({"account_id": ACCT_A}, ["troubleshoot"], _existing(SCOPE_ACCOUNTS), [ACCT_A, ACCT_B])


def test_older_client_keeps_a_single_account_rule_editable():
    result = _resolve_scope({"account_id": ACCT_B}, ["troubleshoot"], _existing(SCOPE_ACCOUNTS), [ACCT_A])
    assert result == (SCOPE_ACCOUNTS, [ACCT_B])


def test_dropping_the_accounts_never_widens_a_rule():
    with pytest.raises(InvalidRuleError):
        _resolve_scope({}, [AUTOMATION_SOURCE], _existing(SCOPE_ACCOUNTS), [ACCT_A])


def test_explicit_all_widens_an_account_optional_rule():
    result = _resolve_scope({"scope_mode": SCOPE_ALL}, [AUTOMATION_SOURCE], _existing(SCOPE_ACCOUNTS), [ACCT_A])
    assert result == (SCOPE_ALL, [])


def test_new_account_scoped_rule_needs_accounts_unless_it_asks_for_all():
    # Older clients never send scope_mode, so they still have to name an account.
    for data in ({}, {"scope_mode": SCOPE_ACCOUNTS, "account_ids": []}):
        with pytest.raises(InvalidRuleError):
            _resolve_scope(data, ["troubleshoot"], None, [])
    assert _resolve_scope({"scope_mode": SCOPE_ALL}, ["troubleshoot"], None, []) == (SCOPE_ALL, [])


def test_existing_all_accounts_rule_stays_editable():
    # Rules saved without an account before scopes existed were migrated to "all".
    assert _resolve_scope({}, ["troubleshoot"], _existing(SCOPE_ALL), []) == (SCOPE_ALL, [])


def test_unknown_scope_mode_is_rejected():
    with pytest.raises(InvalidRuleError):
        _resolve_scope({"scope_mode": "tenant", "account_ids": [ACCT_A]}, ["troubleshoot"], None, [])


# ----------------------------- database fakes -----------------------------


class _Rows:
    def __init__(self, rows):
        self._rows = list(rows)

    def scalars(self):
        return self

    def all(self):
        return self._rows

    def one_or_none(self):
        return self._rows[0] if self._rows else None

    def __iter__(self):
        return iter(self._rows)


class _Session:
    """Answers each statement by the table it reads. `accounts` maps a visible
    account id to its name, as the tenant-filtered cloud_accounts query returns."""

    def __init__(self, rules=(), rule_accounts=(), accounts=None, columns=None, fail_account_rows=False):
        self.rules = list(rules)
        self.rule_accounts = list(rule_accounts)
        self.accounts = accounts or {}
        self.columns = ALL_COLUMNS if columns is None else columns
        self.fail_account_rows = fail_account_rows
        self.statements = []
        self.added = []
        self.deleted = []
        self.committed = False

    async def execute(self, statement, params=None):
        sql = str(statement)
        self.statements.append(sql)
        if isinstance(statement, TextClause):
            return _Rows([(c,) for c in self.columns])
        if "FROM notification_rule_mappings" in sql:
            return _Rows([])
        if "FROM cloud_accounts" in sql:
            return _Rows(list(self.accounts.items()))
        if "FROM notification_rule_accounts" in sql:
            if self.fail_account_rows:
                raise RuntimeError("connection reset")
            columns = sql.split("FROM")[0]
            if "rule_id" in columns and "account_id" in columns and "tenant_id" not in columns:
                return _Rows([(r.rule_id, r.account_id) for r in self.rule_accounts])
            if "rule_id" not in columns:
                return _Rows([r.account_id for r in self.rule_accounts])
            return _Rows(self.rule_accounts)
        if "FROM notification_rules" in sql:
            # A flushed rule is visible to later queries in the same transaction.
            return _Rows(self.rules + [o for o in self.added if isinstance(o, NotificationRules)])
        raise AssertionError(f"unexpected statement: {sql}")

    def add(self, obj):
        self.added.append(obj)

    async def delete(self, obj):
        self.deleted.append(obj)

    async def flush(self):
        pass

    async def commit(self):
        self.committed = True

    async def rollback(self):
        pass


class _SessionFactory:
    def __init__(self, session):
        self._session = session

    def __call__(self):
        return self

    async def __aenter__(self):
        return self._session

    async def __aexit__(self, *exc):
        return False


class _Cache:
    redis_client = object()

    def __init__(self):
        self.cached = []

    def get_notification_rules(self, tenant_id):
        return None

    def cache_notification_rules(self, tenant_id, rules):
        self.cached.append(rules)

    def delete_cached_notification_rules(self, tenant_id):
        pass


def _service(monkeypatch, session):
    monkeypatch.setattr(rules_module.BaseDB, "async_session", staticmethod(lambda engine: _SessionFactory(session)))
    monkeypatch.setattr(rules_module, "create_audit_request", lambda **kwargs: None)
    service = NotificationRulesService(engine=None)
    service.cache = _Cache()
    return service


def _save(service, rule):
    return asyncio.run(
        service.save_or_update_rule(
            action={"name": "notifications_upsert_rule"},
            input={"rule": rule},
            session_variables={"tenant_id": TENANT, "user_id": str(uuid.uuid4())},
        )
    )


def _db_rule(scope_mode, account_id=None):
    return NotificationRules(
        id=uuid.uuid4(),
        tenant_id=TENANT,
        name="Prod alerts",
        source="troubleshoot",
        is_active=True,
        is_suppressed=True,
        scope_mode=scope_mode,
        account_id=account_id,
    )


# ----------------------------- saving -----------------------------


def test_save_writes_one_row_per_account_and_the_mirror(monkeypatch):
    session = _Session(accounts={ACCT_A: "k8s-dev", ACCT_B: "k8s-prod"})
    service = _service(monkeypatch, session)

    result = _save(service, {"name": "Prod alerts", "source": "troubleshoot", "account_ids": [ACCT_A, ACCT_B]})

    assert "error" not in result, result
    [rule] = [o for o in session.added if isinstance(o, NotificationRules)]
    rows = [o for o in session.added if isinstance(o, NotificationRuleAccounts)]
    assert rule.scope_mode == SCOPE_ACCOUNTS
    assert (rule.account_id, rule.cluster) == (ACCT_A, "k8s-dev")
    assert [(str(r.rule_id), r.account_id, r.tenant_id) for r in rows] == [
        (result["id"], ACCT_A, TENANT),
        (result["id"], ACCT_B, TENANT),
    ]
    assert session.committed


def test_save_rejects_an_account_outside_the_tenant(monkeypatch):
    # The cloud_accounts lookup is tenant-filtered, so another tenant's account is not returned.
    session = _Session(accounts={ACCT_A: "k8s-dev"})
    service = _service(monkeypatch, session)

    result = _save(service, {"name": "Prod alerts", "source": "troubleshoot", "account_ids": [ACCT_A, ACCT_C]})

    assert result["status_code"] == 400
    assert ACCT_C in result["error"]
    assert not session.committed
    [lookup] = [s for s in session.statements if "FROM cloud_accounts" in s]
    assert "cloud_accounts.tenant = " in lookup


def test_save_rejects_a_malformed_account_id_before_querying(monkeypatch):
    session = _Session(accounts={ACCT_A: "k8s-dev"})
    service = _service(monkeypatch, session)

    result = _save(service, {"name": "Prod alerts", "source": "troubleshoot", "account_ids": [ACCT_A, "not-a-uuid"]})

    assert result["status_code"] == 400
    assert "not-a-uuid" in result["error"]
    assert not any("FROM cloud_accounts" in s for s in session.statements)


def test_update_replaces_the_account_rows(monkeypatch):
    existing = _db_rule(SCOPE_ACCOUNTS, account_id=ACCT_A)
    old_row = NotificationRuleAccounts(rule_id=existing.id, account_id=ACCT_A, tenant_id=TENANT)
    session = _Session(rules=[existing], rule_accounts=[old_row], accounts={ACCT_B: "k8s-prod", ACCT_C: "k8s-qa"})
    service = _service(monkeypatch, session)

    result = _save(
        service,
        {"id": str(existing.id), "name": "Prod alerts", "source": "troubleshoot", "account_ids": [ACCT_B, ACCT_C]},
    )

    assert "error" not in result, result
    assert session.deleted == [old_row]
    assert [r.account_id for r in session.added if isinstance(r, NotificationRuleAccounts)] == [ACCT_B, ACCT_C]
    assert (existing.account_id, existing.cluster) == (ACCT_B, "k8s-prod")


def test_widening_to_every_account_clears_rows_and_mirror(monkeypatch):
    existing = _db_rule(SCOPE_ACCOUNTS, account_id=ACCT_A)
    existing.source = AUTOMATION_SOURCE
    old_row = NotificationRuleAccounts(rule_id=existing.id, account_id=ACCT_A, tenant_id=TENANT)
    session = _Session(rules=[existing], rule_accounts=[old_row])
    service = _service(monkeypatch, session)

    result = _save(
        service,
        {
            "id": str(existing.id),
            "name": "Silence chatter",
            "source": AUTOMATION_SOURCE,
            "scope_mode": SCOPE_ALL,
            "event_types": ["automation.task.progress"],
        },
    )

    assert "error" not in result, result
    assert existing.scope_mode == SCOPE_ALL
    assert (existing.account_id, existing.cluster) == (None, None)
    assert session.deleted == [old_row]
    assert not [r for r in session.added if isinstance(r, NotificationRuleAccounts)]


# ----------------------------- loading -----------------------------


def test_load_attaches_each_rules_accounts_and_caches(monkeypatch):
    multi, everywhere = _db_rule(SCOPE_ACCOUNTS, ACCT_A), _db_rule(SCOPE_ALL)
    rows = [
        NotificationRuleAccounts(rule_id=multi.id, account_id=uuid.UUID(ACCT_A), tenant_id=TENANT),
        NotificationRuleAccounts(rule_id=multi.id, account_id=uuid.UUID(ACCT_B), tenant_id=TENANT),
    ]
    session = _Session(rules=[multi, everywhere], rule_accounts=rows)
    service = _service(monkeypatch, session)

    loaded = asyncio.run(service.get_notification_rules(TENANT))

    assert loaded[0].account_ids == [ACCT_A, ACCT_B]
    assert loaded[1].account_ids == []
    assert service.cache.cached == [loaded]


def test_failed_account_load_matches_nothing_and_is_not_cached(monkeypatch):
    rule = _db_rule(SCOPE_ACCOUNTS, ACCT_A)
    session = _Session(rules=[rule], fail_account_rows=True)
    service = _service(monkeypatch, session)

    loaded = asyncio.run(service.get_notification_rules(TENANT))

    assert loaded == [rule]
    assert rule.account_ids is None
    assert not _scope_matches(rule, ACCT_A)
    assert service.cache.cached == []


def _degraded_load(monkeypatch, missing_column, rule, rule_accounts):
    """Load through the pre-migration fallback: the first full select fails on
    missing_column, then rules reload with only the columns that exist."""

    class _PgError(Exception):
        sqlstate = "42703"

    session = _Session(
        rules=[rule], rule_accounts=rule_accounts, columns=[c for c in ALL_COLUMNS if c != missing_column]
    )
    original = session.execute
    failed = []

    async def execute(statement, params=None):
        if not failed and "FROM notification_rules" in str(statement) and not isinstance(statement, TextClause):
            failed.append(True)
            raise ProgrammingError("SELECT ...", {}, _PgError())
        return await original(statement, params)

    session.execute = execute
    service = _service(monkeypatch, session)
    return asyncio.run(service.get_notification_rules(TENANT)), session, service


def test_load_missing_only_a_later_column_still_uses_account_rows(monkeypatch):
    rule = _db_rule(SCOPE_ACCOUNTS, ACCT_A)
    rows = [NotificationRuleAccounts(rule_id=rule.id, account_id=uuid.UUID(ACCT_B), tenant_id=TENANT)]

    loaded, _, service = _degraded_load(monkeypatch, "event_types", rule, rows)

    assert loaded[0].account_ids == [ACCT_B]
    assert _scope_matches(loaded[0], ACCT_B)
    assert not _scope_matches(loaded[0], ACCT_A)
    assert service.cache.cached == []


def test_load_before_scope_mode_skips_account_rows_and_uses_account_id(monkeypatch):
    rule = _db_rule(None, ACCT_A)

    loaded, session, _ = _degraded_load(monkeypatch, "scope_mode", rule, [])

    assert not any("notification_rule_accounts" in s for s in session.statements)
    assert _scope_matches(loaded[0], ACCT_A)
    assert not _scope_matches(loaded[0], ACCT_B)
